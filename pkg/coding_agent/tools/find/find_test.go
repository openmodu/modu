package find

import "testing"

func TestMatchFindPatternSupportsDoublestar(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		relPath string
		want    bool
	}{
		{
			name:    "root file",
			pattern: "**/*.go",
			relPath: "main.go",
			want:    true,
		},
		{
			name:    "nested file",
			pattern: "src/**/*.go",
			relPath: "src/pkg/internal/main.go",
			want:    true,
		},
		{
			name:    "wrong prefix",
			pattern: "src/**/*.go",
			relPath: "cmd/main.go",
			want:    false,
		},
		{
			name:    "basename pattern still matches nested",
			pattern: "*.go",
			relPath: "pkg/mod.go",
			want:    true,
		},
		{
			name:    "single star does not cross directories",
			pattern: "src/*.go",
			relPath: "src/pkg/main.go",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchFindPattern(tt.pattern, tt.relPath, baseName(tt.relPath)); got != tt.want {
				t.Fatalf("matchFindPattern(%q, %q) = %v, want %v", tt.pattern, tt.relPath, got, tt.want)
			}
		})
	}
}

func baseName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
