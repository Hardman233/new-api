package constant

import "testing"

func TestPath2RelayModeRecognizesVideoRoutes(t *testing.T) {
	tests := []struct {
		path string
		want int
	}{
		{path: "/v1/videos", want: RelayModeVideoSubmit},
		{path: "/v1/videos/", want: RelayModeVideoSubmit},
		{path: "/v1/videos/extensions", want: RelayModeVideoSubmit},
		{path: "/v1/videos/task_123/remix", want: RelayModeVideoSubmit},
		{path: "/v1/videos/task_123/extend", want: RelayModeVideoSubmit},
		{path: "/v1/video/generations", want: RelayModeVideoSubmit},
		{path: "/v1/videos/task_123", want: RelayModeVideoFetchByID},
		{path: "/v1/video/generations/task_123", want: RelayModeVideoFetchByID},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := Path2RelayMode(test.path); got != test.want {
				t.Fatalf("Path2RelayMode(%q) = %d, want %d", test.path, got, test.want)
			}
		})
	}
}
