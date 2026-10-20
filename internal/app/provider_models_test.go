package app

import (
	"reflect"
	"testing"
)

func TestParseUpstreamModelIDs(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    []string
		wantErr bool
	}{
		{
			name: "openai envelope is sorted and deduped",
			body: `{"object":"list","data":[{"id":"qwen-plus"},{"id":"qwen-turbo"},{"id":"qwen-plus"}]}`,
			want: []string{"qwen-plus", "qwen-turbo"},
		},
		{
			name: "bare array is accepted",
			body: `[{"id":"b"},{"id":"a"}]`,
			want: []string{"a", "b"},
		},
		{
			name: "entries without an id are skipped",
			body: `{"data":[{"id":"  "},{"object":"model"},{},{"id":" z "}]}`,
			want: []string{"z"},
		},
		{
			name:    "an object that is not a model list is an error",
			body:    `{"error":{"message":"denied"}}`,
			wantErr: true,
		},
		{
			name:    "a malformed body is an error",
			body:    `not json`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseUpstreamModelIDs([]byte(tc.body))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseUpstreamModelIDs(%s) = %v, want an error", tc.body, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUpstreamModelIDs(%s): %v", tc.body, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseUpstreamModelIDs(%s) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}
