package proofpack

import (
	"reflect"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/footprintai/containarium/internal/audit"
	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// signerFields are field names that would make a sealed artifact depend on a
// guardrail signing key: rotating or revoking the key (#2729) could then
// invalidate it.
var signerFields = map[protoreflect.Name]bool{"signature": true, "public_key": true, "key_id": true}

// signerDependence returns the first field reachable from md that holds a
// signature or a key, as "Message.field", or "" when there is none.
func signerDependence(md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) string {
