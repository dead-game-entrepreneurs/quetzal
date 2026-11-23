package quazal

import "testing"

func TestWithRVCIDPreservesNativeProbeIdentity(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"prudp:/address=192.0.2.10;port=9103;CID=1", "prudp:/address=192.0.2.10;port=9103;CID=1;RVCID=1"},
		{"prudps:/address=192.0.2.20;port=9103;CID=2", "prudps:/address=192.0.2.20;port=9103;CID=2;RVCID=2"},
		{"prudp:/CID=1;RVCID=7", "prudp:/CID=1;RVCID=7"},
		{"prudp:/address=192.0.2.10;port=9103", "prudp:/address=192.0.2.10;port=9103"},
		{"prudp:/CID=0", "prudp:/CID=0"},
		{"prudp:/CID=invalid", "prudp:/CID=invalid"},
	} {
		got := WithRVCID(test.input)
		if got != test.want || WithRVCID(got) != got {
			t.Fatalf("probe URL %q -> %q, want %q", test.input, got, test.want)
		}
	}
}
