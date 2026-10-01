package config

import "testing"

func TestJSONEscapedSlashesPreserveSourceSemantics(t *testing.T) {
	c, err := Parse([]byte(`{"version":1,"requests":[{"id":"x","protocol":"http","action":"POST","endpoint":"http:\/\/127.0.0.1:48416\/status","params":{"topic":"\/sensors\/\/temp\/","literal":"\\/","value":18446744073709551615,"body":"中文😀"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	r := c.Requests[0]
	if r.Endpoint != "http://127.0.0.1:48416/status" || r.Params["topic"] != "/sensors//temp/" || r.Params["literal"] != `\/` || r.Params["body"] != "中文😀" {
		t.Fatalf("escaped JSON changed meaning: %#v", r)
	}
	if r.Params["value"] != uint64(18446744073709551615) {
		t.Fatalf("integer precision lost: %#v", r.Params["value"])
	}
}
func TestJSONSlashNormalizationKeepsStrictValidation(t *testing.T) {
	for _, s := range []string{
		`{"version":1,"version":1,"requests":[{"id":"x","protocol":"http","action":"GET","endpoint":"http:\/\/local"}]}`,
		`{"version":1,"unexpected":true,"requests":[{"id":"x","protocol":"http","action":"GET","endpoint":"http:\/\/local"}]}`,
		"version: 1\nrequests:\n - id: x\n   protocol: http\n   action: GET\n   endpoint: \"http:\\/\\/local\"\n",
	} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Fatalf("accepted invalid or ambiguous collection: %s", s)
		}
	}
}

func TestJSONSlashNormalizationHandlesEscapedPairsAndUnicode(t *testing.T) {
	c, err := Parse([]byte(`{"version":1,"requests":[{"id":"x","protocol":"http","action":"GET","endpoint":"http:\/\/local","params":{"body":"\u4e2d\u6587","escaped":"\\\/","quote":"x\"\/y"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p := c.Requests[0].Params
	if p["body"] != "中文" || p["escaped"] != `\/` || p["quote"] != `x"/y` {
		t.Fatalf("escape pairs changed: %#v", p)
	}
	if _, err := Parse([]byte(`{"version":1,"requests":[{"id":"x","protocol":"http","action":"GET","endpoint":"http:\/\/local\q"}]}`)); err == nil {
		t.Fatal("accepted unknown JSON/YAML escape")
	}
	c, err = Parse([]byte("version: 1\nrequests:\n - id: x\n   protocol: http\n   action: GET\n   endpoint: http://local\n   params:\n     literal: '\\/'\n"))
	if err != nil || c.Requests[0].Params["literal"] != `\/` {
		t.Fatalf("changed YAML single-quoted literal: %v %#v", err, c)
	}
}
