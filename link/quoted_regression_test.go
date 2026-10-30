package link

import "testing"

func TestQuotedEscapedControlRejected(t *testing.T) {
	for _, c := range []byte{0, 0x0f, 0x7f} {
		input := "</>;x=\"\\" + string(c) + "\""
		if _, err := Parse(input); err == nil {
			t.Errorf("accepted escaped control %q", input)
		}
	}
}
