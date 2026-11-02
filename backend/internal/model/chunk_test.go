package model

import "testing"

func TestChunkUploadSessionExpectedChunkSize(t *testing.T) {
	sess := &ChunkUploadSession{
		FileSize:    12 << 20,
		ChunkSize:   5 << 20,
		TotalChunks: 3,
	}
	cases := []struct {
		index int
		want  int64
	}{
		{0, 5 << 20},
		{1, 5 << 20},
		{2, 2 << 20},
	}
	for _, tc := range cases {
		if got := sess.ExpectedChunkSize(tc.index); got != tc.want {
			t.Fatalf("ExpectedChunkSize(%d) = %d, want %d", tc.index, got, tc.want)
		}
	}
}

func TestChunkUploadSessionIsAllDone(t *testing.T) {
	cases := []struct {
		name string
		sess ChunkUploadSession
		want bool
	}{
		{name: "empty session", sess: ChunkUploadSession{}, want: false},
		{name: "bits length mismatch", sess: ChunkUploadSession{TotalChunks: 2, UploadedBits: []bool{true}}, want: false},
		{name: "partial", sess: ChunkUploadSession{TotalChunks: 2, UploadedBits: []bool{true, false}}, want: false},
		{name: "all done", sess: ChunkUploadSession{TotalChunks: 2, UploadedBits: []bool{true, true}}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sess.IsAllDone(); got != tc.want {
				t.Fatalf("IsAllDone() = %v, want %v", got, tc.want)
			}
		})
	}
}
