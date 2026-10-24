package status

import "testing"

func TestCombine(t *testing.T) {
	cases := []struct {
		log, conn, want string
	}{
		{LogRecent, ConnUDP, Healthy},
		{LogRecent, ConnConnected, Healthy},
		{LogRecent, ConnDisconnected, Attention},
		{LogRecent, ConnUnknown, Unknown},
		{LogStale, ConnConnected, Attention},
		{LogStale, ConnUnknown, Attention},
		{LogEmpty, ConnUDP, Attention},
		{LogMissing, ConnUnknown, Attention},
		{LogUnknown, ConnUDP, Unknown},
		{LogUnknown, ConnDisconnected, Attention},
		{LogUnknown, ConnConnected, Unknown},
		{"other", ConnUDP, Unknown},
	}
	for _, tc := range cases {
		if got := Combine(tc.log, tc.conn); got != tc.want {
			t.Errorf("Combine(%s, %s) = %s, want %s", tc.log, tc.conn, got, tc.want)
		}
	}
}
