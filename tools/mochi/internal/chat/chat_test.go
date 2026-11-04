package chat

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/timche/mac-mini/discord"
	"github.com/timche/mac-mini/discord/rest"
)

type channelAPI struct {
	API
	channel *discord.Channel
	err     error
}

func (a channelAPI) Channel(string) (*discord.Channel, error) { return a.channel, a.err }

// A refusal says which of these it is, since each has a different fix.
func TestInGuildSaysWhyAChannelIsRefused(t *testing.T) {
	const guild, channel = "1220378778665095291", "1378670204795617340"
	for name, c := range map[string]struct {
		api  channelAPI
		want string
	}{
		"deleted":       {channelAPI{err: &rest.Error{Status: http.StatusNotFound, Code: rest.CodeUnknownChannel, Message: "Unknown Channel"}}, "does not exist"},
		"out of reach":  {channelAPI{err: &rest.Error{Status: http.StatusForbidden, Code: rest.CodeMissingAccess, Message: "Missing Access"}}, "cannot see"},
		"a failure":     {channelAPI{err: &rest.Error{Status: http.StatusBadGateway}}, "could not be read"},
		"another guild": {channelAPI{channel: &discord.Channel{ID: channel, GuildID: "1220378778665095299"}}, "not in this server"},
	} {
		err := InGuild(c.api, channel, guild)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), channel) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := InGuild(channelAPI{channel: &discord.Channel{ID: channel, GuildID: guild}}, channel, guild); err != nil {
		t.Errorf("in the server: %v", err)
	}
}

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
