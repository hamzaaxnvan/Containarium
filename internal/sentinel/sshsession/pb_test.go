package sshsession

import (
	"reflect"
	"testing"
	"time"

	pb "github.com/footprintai/containarium/pkg/pb/containarium/v1"
)

var allPhases = []SessionPhase{SessionPhaseOpen, SessionPhaseClose, SessionPhaseDenied, SessionPhaseAuthFailed, SessionPhaseBanned}
var allAuthMethods = []AuthMethod{AuthMethodCertificate, AuthMethodPublicKey, AuthMethodUnknown}
var allCloseReasons = []CloseReason{
	CloseReasonNormal, CloseReasonUpstreamGone, CloseReasonProxyShutdown,
	CloseReasonError, CloseReasonUnknownOrphan,
}

func baseRecord() Record {
	return Record{
		SessionID:  "sess-1",
		Phase:      SessionPhaseOpen,
		OccurredAt: time.Date(2026, 10, 9, 1, 2, 3, 456000000, time.UTC),
		ClientIP:   "203.0.113.9",
		ClientPort: 40222,
		Login:      "alice",
		Target:     "10.0.0.5:22",
		AuthMethod: AuthMethodCertificate,
		Credential: Credential{KeyID: "kid", Serial: 7, CAFingerprint: "SHA256:ca"},
	}
}

func TestToFromProto_RoundTripEveryEnumValue(t *testing.T) {
	for _, ph := range allPhases {
		for _, am := range allAuthMethods {
			r := baseRecord()
			r.Phase, r.AuthMethod = ph, am
			if ph == SessionPhaseClose {
				r.CloseReason = CloseReasonNormal
			}
			if ph == SessionPhaseAuthFailed || ph == SessionPhaseBanned {
				// Source-IP records carry no session identity (#2419).
				if am != AuthMethodCertificate {
					continue
				}
				r = sourceRecord(ph)
			}
			got, err := FromProto(ToProto(r))
			if err != nil {
				t.Fatalf("%s/%s: %v", ph, am, err)
			}
			if !reflect.DeepEqual(got, r) {
				t.Fatalf("%s/%s round trip:\n got %+v\nwant %+v", ph, am, got, r)
			}
		}
	}
	for _, cr := range allCloseReasons {
		r := baseRecord()
		r.Phase, r.CloseReason = SessionPhaseClose, cr
		got, err := FromProto(ToProto(r))
		if err != nil || !reflect.DeepEqual(got, r) {
			t.Fatalf("close_reason %s: got %+v err %v", cr, got, err)
		}
	}
}

func TestToFromProto_RawKeyCredentialAndNoCredential(t *testing.T) {
	r := baseRecord()
	r.AuthMethod = AuthMethodPublicKey
	r.Credential = Credential{KeyFingerprint: "SHA256:key"}
	if got, err := FromProto(ToProto(r)); err != nil || !reflect.DeepEqual(got, r) {
