package shared

import "testing"

func TestIsMetaKeyIDIdentifier(t *testing.T) {
	cases := []struct {
		name       string
		identifier string
		want       bool
	}{
		{name: "数字 ID", identifier: "12", want: true},
		{name: "空值", identifier: "", want: false},
		{name: "MetaName", identifier: "standard_delivery_days", want: false},
		{name: "混合字符串", identifier: "12_days", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isMetaKeyIDIdentifier(tc.identifier)
			if got != tc.want {
				t.Fatalf("isMetaKeyIDIdentifier(%q) = %v, want %v", tc.identifier, got, tc.want)
			}
		})
	}
}
