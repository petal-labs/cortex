package cmd

import "testing"

func TestServeHostFlagDefault(t *testing.T) {
	f := serveCmd.Flags().Lookup("host")
	if f == nil {
		t.Fatal("expected --host flag on serve command")
	}
	if f.DefValue != "127.0.0.1" {
		t.Errorf("expected --host default to be 127.0.0.1, got %q", f.DefValue)
	}
}

func TestResolveServeBindHost(t *testing.T) {
	tests := []struct {
		name        string
		flagHost    string
		flagChanged bool
		cfgHost     string
		want        string
	}{
		{
			name: "defaults to 127.0.0.1 when both empty",
			want: "127.0.0.1",
		},
		{
			name:     "uses config host when flag not explicitly set",
			flagHost: "127.0.0.1",
			cfgHost:  "0.0.0.0",
			want:     "0.0.0.0",
		},
		{
			name:        "CLI flag overrides config host when explicitly changed",
			flagHost:    "::1",
			flagChanged: true,
			cfgHost:     "0.0.0.0",
			want:        "::1",
		},
		{
			name:     "falls back to flag default when config empty",
			flagHost: "127.0.0.1",
			cfgHost:  "",
			want:     "127.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveServeBindHost(tt.flagHost, tt.flagChanged, tt.cfgHost)
			if got != tt.want {
				t.Errorf("resolveServeBindHost(%q, %v, %q) = %q, want %q",
					tt.flagHost, tt.flagChanged, tt.cfgHost, got, tt.want)
			}
		})
	}
}
