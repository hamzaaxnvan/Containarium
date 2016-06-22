package sshsession

import (
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

// Conversions between the sentinel's on-disk Record (JSONL) and the
// AuditService wire type (#2415). Table-driven in both directions so a new
// Go constant without a proto value (or the reverse) is caught by
// TestEnumSetsMatchProto rather than at ingest time.

var phaseToPB = map[SessionPhase]pb.SSHSessionPhase{
	SessionPhaseOpen:   pb.SSHSessionPhase_SSH_SESSION_PHASE_OPEN,
	SessionPhaseClose:  pb.SSHSessionPhase_SSH_SESSION_PHASE_CLOSE,
	SessionPhaseDenied: pb.SSHSessionPhase_SSH_SESSION_PHASE_DENIED,

	SessionPhaseAuthFailed: pb.SSHSessionPhase_SSH_SESSION_PHASE_AUTH_FAILED,
	SessionPhaseBanned:     pb.SSHSessionPhase_SSH_SESSION_PHASE_BANNED,
}

var authMethodToPB = map[AuthMethod]pb.SSHAuthMethod{
	AuthMethodCertificate: pb.SSHAuthMethod_SSH_AUTH_METHOD_CERTIFICATE,
	AuthMethodPublicKey:   pb.SSHAuthMethod_SSH_AUTH_METHOD_PUBLICKEY,
	AuthMethodUnknown:     pb.SSHAuthMethod_SSH_AUTH_METHOD_UNKNOWN,
}

var closeReasonToPB = map[CloseReason]pb.SSHCloseReason{
	CloseReasonNormal:        pb.SSHCloseReason_SSH_CLOSE_REASON_NORMAL,
	CloseReasonUpstreamGone:  pb.SSHCloseReason_SSH_CLOSE_REASON_UPSTREAM_GONE,
	CloseReasonProxyShutdown: pb.SSHCloseReason_SSH_CLOSE_REASON_PROXY_SHUTDOWN,
	CloseReasonError:         pb.SSHCloseReason_SSH_CLOSE_REASON_ERROR,
	CloseReasonUnknownOrphan: pb.SSHCloseReason_SSH_CLOSE_REASON_UNKNOWN_ORPHAN,
}

func invert[K comparable, V comparable](m map[K]V) map[V]K {
	out := make(map[V]K, len(m))
	for k, v := range m {
		out[v] = k
	}
	return out
}

var (
	phaseFromPB       = invert(phaseToPB)
	authMethodFromPB  = invert(authMethodToPB)
	closeReasonFromPB = invert(closeReasonToPB)
)

// ToProto converts a Record read from the sink into its wire form. A value
// with no proto counterpart (a hand-edited file) maps to the UNSPECIFIED
// enum value, which the server rejects — the shipper validates with
// FromProto before sending, so that never reaches the wire.
func ToProto(r Record) *pb.SSHSessionRecord {
	out := &pb.SSHSessionRecord{
		SessionId:     r.SessionID,
		Phase:         phaseToPB[r.Phase],
		OccurredAt:    timestamppb.New(r.OccurredAt),
		ClientIp:      r.ClientIP,
		ClientPort:    int32(r.ClientPort), // #nosec G115 -- a TCP port fits int32
		Login:         r.Login,
		Target:        r.Target,
		AuthMethod:    authMethodToPB[r.AuthMethod],
		CloseReason:   closeReasonToPB[r.CloseReason],
		UserId:        r.UserID,
		AuthzDecision: r.AuthzDecision,
		DenyReason:    r.DenyReason,
		Enforced:      r.Enforced,

		FailureCount:    r.FailureCount,
		BanListener:     r.BanListener,
		SuppressedCount: r.SuppressedCount,
	}
	if r.BannedUntil != nil {
		out.BannedUntil = timestamppb.New(*r.BannedUntil)
	}
	if r.Credential != (Credential{}) {
		out.Credential = &pb.SSHSessionCredential{
			KeyId:            r.KeyID,
			Serial:           r.Serial,
