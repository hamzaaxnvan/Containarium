package netbpf

import "testing"

func TestHostVethFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]string
		want   string
	}{
		{
			name:   "primary eth0",
			config: map[string]string{"volatile.eth0.host_name": "veth1a2b3c4d", "volatile.eth0.hwaddr": "00:16:3e:aa:bb:cc"},
			want:   "veth1a2b3c4d",
		},
		{
			name:   "eth0 preferred over other nics",
			config: map[string]string{"volatile.eth1.host_name": "vethZZZZ", "volatile.eth0.host_name": "vethEEEE"},
			want:   "vethEEEE",
		},
		{
			name:   "non-eth0 nic falls back to lexicographically first",
