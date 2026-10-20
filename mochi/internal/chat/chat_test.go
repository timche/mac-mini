package chat

import (
	"errors"
	"net/http"
	"net/url"
	"testing"
)

type refuse struct{ t *testing.T }

func (r refuse) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("a request went out to %s", req.URL)
	return nil, errors.New("refused")
}

func TestOnlyDiscordsCDNIsFetched(t *testing.T) {
	l := Live{Download: &http.Client{Transport: refuse{t}, CheckRedirect: stayOnCDN}}

	for _, raw := range []string{
		"http://cdn.discordapp.com/attachments/1/2/a.png",
		"https://evil.example/a.png",
		"https://cdn.discordapp.com.evil.example/a.png",
		"https://cdn.discordapp.com:8443/a.png",
		"https://user@cdn.discordapp.com/a.png",
		"file:///etc/passwd",
		"http://169.254.169.254/latest/meta-data",
	} {
		if _, err := l.Fetch(raw); err == nil {
			t.Errorf("fetched %s", raw)
		}
	}

	for _, raw := range []string{
		"https://cdn.discordapp.com/attachments/1/2/a.png?ex=1",
		"https://media.discordapp.net/attachments/1/2/a.png",
	} {
		u, _ := url.Parse(raw)
		if !OnCDN(u) {
			t.Errorf("refused %s", raw)
		}
	}
}

func TestARedirectOffTheCDNIsRefused(t *testing.T) {
	to := func(raw string) *http.Request {
		u, _ := url.Parse(raw)
		return &http.Request{URL: u}
	}
	if stayOnCDN(to("https://evil.example/x"), nil) == nil {
		t.Error("followed a redirect off the CDN")
	}
	if stayOnCDN(to("http://cdn.discordapp.com/x"), nil) == nil {
		t.Error("followed a redirect to plain http")
	}
	if err := stayOnCDN(to("https://media.discordapp.net/x"), nil); err != nil {
		t.Errorf("refused a redirect within the CDN: %v", err)
	}
}
