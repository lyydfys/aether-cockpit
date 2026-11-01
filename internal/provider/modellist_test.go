package provider

import (
	"reflect"
	"testing"
)

func TestParseModelListIDs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "openai data",
			raw:  `{"object":"list","data":[{"id":"gpt-5"},{"id":"glm-5"},{"id":"gpt-5"}]}`,
			want: []string{"gpt-5", "glm-5"},
		},
		{
			name: "ollama models",
			raw:  `{"models":[{"name":"llama3:8b"},{"model":"qwen3"}]}`,
			want: []string{"llama3:8b", "qwen3"},
		},
		{
			name: "bare array",
			raw:  `["m1",{"id":"m2"},"m1"]`,
			want: []string{"m1", "m2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseModelListIDs([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	for _, raw := range []string{`{"error":"nope"}`, `{"data":[]}`, `not json`} {
		if _, err := ParseModelListIDs([]byte(raw)); err == nil {
			t.Fatalf("%s 应当报错", raw)
		}
	}
}
