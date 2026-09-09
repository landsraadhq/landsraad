package catalog

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{in: "service:ledger-api", want: Ref{Kind: KindService, Name: "ledger-api"}},
		{in: "topic:payments.events", want: Ref{Kind: KindTopic, Name: "payments.events"}},
		{in: "database:payments-pg", want: Ref{Kind: KindDatabase, Name: "payments-pg"}},
		{in: "api:billing", want: Ref{Kind: KindAPI, Name: "billing"}},
		{in: "Service:ledger-api", want: Ref{Kind: KindService, Name: "ledger-api"}},
		{in: "noprefix", wantErr: true},
		{in: "widget:thing", wantErr: true},
		{in: "service:", wantErr: true},
		{in: ":ledger-api", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseRef(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseRef(%q) = %v, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRef(%q) returned error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseRef(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestRefStringIsLowercasePrefixed(t *testing.T) {
	r := Ref{Kind: KindService, Name: "ledger-api"}
	if got := r.String(); got != "service:ledger-api" {
		t.Errorf("String() = %q, want %q", got, "service:ledger-api")
	}
}

func TestRefRoundTrips(t *testing.T) {
	for _, k := range AllKinds() {
		in := Ref{Kind: k, Name: "thing"}
		out, err := ParseRef(in.String())
		if err != nil {
			t.Errorf("round trip of %v failed: %v", in, err)
			continue
		}
		if out != in {
			t.Errorf("round trip of %v gave %v", in, out)
		}
	}
}
