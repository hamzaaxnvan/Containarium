package k8s

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"

	"github.com/footprintai/containarium/pkg/core/volume"
)

const (
	sharedVolumeLabel     = "containarium.dev/shared-volume"
	sharedVolumeNameLabel = "containarium.dev/shared-volume-name"
	sharedVolumeNameMax   = 56 // leaves room for the "shared-" pod volume prefix
)

var _ volume.Backend = (*Backend)(nil)

// SharedVolumesSupported reports whether an RWX StorageClass was configured.
// Kubernetes does not publish access modes on StorageClass objects, so the
// operator explicitly selects a class whose provisioner supports RWX.
func (b *Backend) SharedVolumesSupported(_ context.Context, _ string) (string, bool, string) {
	class := strings.TrimSpace(b.cfg.SharedVolumeStorageClass)
	if class == "" {
		return "", false, "no RWX shared-volume StorageClass configured; set CONTAINARIUM_K8S_SHARED_VOLUME_STORAGE_CLASS"
	}
	return class, true, "RWX StorageClass: " + class
}

func (b *Backend) CreateVolume(ctx context.Context, tenant, name string, sizeBytes int64, pool string) (*volume.Volume, error) {
	resolvedPool, err := b.resolveSharedVolumeClass(ctx, pool)
	if err != nil {
		return nil, err
	}
	pool = resolvedPool
	if err := b.validateTenantName(tenant); err != nil {
		return nil, err
	}
	if err := validateSharedVolumeName(name); err != nil {
		return nil, err
	}
	if sizeBytes <= 0 {
		return nil, fmt.Errorf("size_bytes must be > 0")
	}

	ns := b.namespaceFor(tenant)
	if _, err := b.clientset.CoreV1().Namespaces().Create(ctx, namespaceObject(ns, tenant), metav1.CreateOptions{}); ignoreExists(err) != nil {
		return nil, fmt.Errorf("k8s: ensure shared-volume namespace: %w", err)
	}
	nameInK8s := sharedVolumePVCName(name)
	claims := b.clientset.CoreV1().PersistentVolumeClaims(ns)
	if existing, err := claims.Get(ctx, nameInK8s, metav1.GetOptions{}); err == nil {
		if err := validateExistingSharedPVC(existing, name, pool, sizeBytes); err != nil {
			return nil, err
		}
		return b.sharedVolumeFromPVC(ctx, tenant, existing)
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("k8s: get shared volume PVC: %w", err)
	}

	quantity := *resource.NewQuantity(sizeBytes, resource.BinarySI)
	class := pool
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nameInK8s,
			Namespace: ns,
			Labels: map[string]string{
				managedByLabel:        managedByValue,
				tenantLabel:           tenant,
				sharedVolumeLabel:     "true",
				sharedVolumeNameLabel: name,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: quantity}},
			StorageClassName: &class,
		},
	}
	created, err := claims.Create(ctx, pvc, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		created, err = claims.Get(ctx, nameInK8s, metav1.GetOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("k8s: create shared volume PVC: %w", err)
	}
	if err := validateExistingSharedPVC(created, name, pool, sizeBytes); err != nil {
		return nil, err
	}
	return b.sharedVolumeFromPVC(ctx, tenant, created)
}

func (b *Backend) ListVolumes(ctx context.Context, tenant, pool string) ([]volume.Volume, error) {
	resolvedPool, err := b.resolveSharedVolumeClass(ctx, pool)
	if err != nil {
		return nil, err
	}
	pool = resolvedPool
	if err := b.validateTenantName(tenant); err != nil {
		return nil, err
	}
	selector := labels.Set{managedByLabel: managedByValue, tenantLabel: tenant, sharedVolumeLabel: "true"}.AsSelector().String()
	list, err := b.clientset.CoreV1().PersistentVolumeClaims(b.namespaceFor(tenant)).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return []volume.Volume{}, nil
		}
		return nil, fmt.Errorf("k8s: list shared volume PVCs: %w", err)
	}
	volumes := make([]volume.Volume, 0, len(list.Items))
	for i := range list.Items {
		v, err := b.sharedVolumeFromPVC(ctx, tenant, &list.Items[i])
		if err != nil {
			return nil, err
		}
		if v.Pool == pool {
			volumes = append(volumes, *v)
		}
	}
	return volumes, nil
}

func (b *Backend) GetVolume(ctx context.Context, tenant, name, pool string) (*volume.Volume, error) {
	if err := validateSharedVolumeName(name); err != nil {
		return nil, err
	}
	volumes, err := b.ListVolumes(ctx, tenant, pool)
	if err != nil {
		return nil, err
	}
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i], nil
		}
	}
	return nil, fmt.Errorf("volume %q not found", name)
}

func (b *Backend) DeleteVolume(ctx context.Context, tenant, name, pool string, force bool) error {
	v, err := b.GetVolume(ctx, tenant, name, pool)
	if err != nil {
		return err
	}
	if len(v.Attachments) > 0 && !force {
		return fmt.Errorf("volume %q is still attached to %d container(s); detach first or pass force", name, len(v.Attachments))
	}
	if force && len(v.Attachments) > 0 {
		for _, attachment := range v.Attachments {
			if err := b.DetachVolume(ctx, tenant, name, attachment.Container); err != nil {
				return fmt.Errorf("k8s: force-detach shared volume %q: %w", name, err)
			}
		}
	}
	if err := b.clientset.CoreV1().PersistentVolumeClaims(b.namespaceFor(tenant)).Delete(ctx, sharedVolumePVCName(name), metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("k8s: delete shared volume PVC: %w", err)
	}
	return nil
}

func (b *Backend) AttachVolume(ctx context.Context, tenant, name, pool, container, mountPath string, readOnly bool) error {
	if _, err := b.resolveSharedVolumeClass(ctx, pool); err != nil {
		return err
	}
	if err := validateSharedVolumeName(name); err != nil {
		return err
	}
	cleanPath := path.Clean(mountPath)
	if !path.IsAbs(mountPath) || cleanPath == "/" {
		return fmt.Errorf("mount_path must be an absolute path other than /")
	}
	if reservedMountPath(cleanPath) {
		return fmt.Errorf("mount_path %q overlaps a platform-managed mount", cleanPath)
	}
	if _, err := b.GetVolume(ctx, tenant, name, pool); err != nil {
		return err
	}
	ns := b.namespaceFor(tenant)
	targetSandbox, err := b.sandboxNameForVolume(ctx, tenant, container)
	if err != nil {
		return err
	}
	sb, err := b.sandboxes.AgentsV1beta1().Sandboxes(ns).Get(ctx, targetSandbox, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("k8s: get tenant sandbox %q for volume attach: %w", targetSandbox, err)
	}
	volName := sharedVolumePodName(name)
	pvcName := sharedVolumePVCName(name)
	if len(sb.Spec.PodTemplate.Spec.Containers) == 0 {
		return fmt.Errorf("k8s: tenant sandbox has no pod container to mount a shared volume into")
	}
	for _, v := range sb.Spec.PodTemplate.Spec.Volumes {
		if v.Name == volName {
			if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == pvcName {
				for _, m := range sb.Spec.PodTemplate.Spec.Containers[0].VolumeMounts {
					if m.Name == volName && m.MountPath == cleanPath && m.ReadOnly == readOnly {
						return nil
					}
				}
			}
			return fmt.Errorf("k8s: shared volume %q already has a conflicting pod volume", name)
		}
	}
	for _, m := range sb.Spec.PodTemplate.Spec.Containers[0].VolumeMounts {
		if mountPathsOverlap(m.MountPath, cleanPath) {
			return fmt.Errorf("k8s: mount path %q is already in use", cleanPath)
		}
	}
	sb.Spec.PodTemplate.Spec.Volumes = append(sb.Spec.PodTemplate.Spec.Volumes, corev1.Volume{
		Name:         volName,
		VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName, ReadOnly: readOnly}},
	})
	sb.Spec.PodTemplate.Spec.Containers[0].VolumeMounts = append(sb.Spec.PodTemplate.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: volName, MountPath: cleanPath, ReadOnly: readOnly})
