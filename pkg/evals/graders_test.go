package evals

import "testing"

func TestExtractJSONObject(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain json",
			in:   `{"pass":true}`,
			want: `{"pass":true}`,
		},
		{
			name: "fenced explanation",
			in:   "Here is the result:\n```json\n{\"pass\":true}\n```",
			want: `{"pass":true}`,
		},
		{
			name: "no json",
			in:   "not json",
			want: "not json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractJSONObject(tc.in); got != tc.want {
				t.Fatalf("extractJSONObject() = %q, want %q", got, tc.want)
			}
		})
	}
}
