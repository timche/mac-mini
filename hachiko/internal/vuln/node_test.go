package vuln

import (
	"testing"

	"github.com/timche/mac-mini/hachiko/internal/harness"
)

// node's own index, newest first, as it really comes: every release in it, security or not,
// and the security releases of three major lines at once.
const nodeIndexJSON = `[
{"version":"v26.12.1","date":"2026-10-21","npm":"11.20.0","lts":false,"security":true},
{"version":"v26.12.0","date":"2026-10-14","npm":"11.20.0","lts":false,"security":false},
{"version":"v24.22.1","date":"2026-10-20","npm":"11.18.2","lts":"Krypton","security":true},
{"version":"v24.22.0","date":"2026-10-07","npm":"11.18.2","lts":"Krypton","security":false},
{"version":"v24.18.1","date":"2026-07-28","npm":"11.10.0","lts":"Krypton","security":true},
{"version":"v22.21.0","date":"2026-09-01","npm":"10.9.4","lts":"Jod","security":true}
]`

// The global config requests `lts`, so `mise up` takes the newest release inside the line and
// never crosses a major.
func (b *box) nodeAt(version string) {
	b.says(miseLsHome, `{"node":[`+miseEntry(version, "/installs/node/"+version, true)+`]}`)
	b.serves(nodeIndexURL, nodeIndexJSON)
}

func TestASecurityReleaseAboveTheInstalledNodeInItsOwnLineIsAnExposure(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.nodeAt("24.22.0")

	e := b.look().exposure(t, "node-v24.22.1")

	harness.Equal(t, e.Name, "node", "what it is about")
	harness.Equal(t, e.Version, "24.22.0", "what is installed")
	harness.Equal(t, e.Fixed, "24.22.1", "the release carrying the fix")
	harness.Equal(t, e.Target, "mise:node", "the target")
	harness.Equal(t, e.Summary, "node released 24.22.1 as a security release on 2026-10-20", "the summary")

	// Two security releases above it in the line is still one thing to move to, the newest.
	behind := newBox(t)
	behind.clean()
	behind.nodeAt("24.17.0")

	found := behind.look()
	harness.Equal(t, len(found.Exposures), 1, "exposures, there being one line to move along")
	harness.Equal(t, found.exposure(t, "node-v24.22.1").Fixed, "24.22.1", "the newest of the two above it")
}

// A fix that is only in a newer major is a decision about which node this Mac runs, which is
// nobody's to make at four in the morning.
func TestAFixOnlyInANewerMajorIsNotSomethingThisStarts(t *testing.T) {
	b := newBox(t)
	b.clean()
	b.nodeAt("24.22.1")

	found := b.look()

	harness.Equal(t, len(found.Exposures), 0, "exposures, 26.12.1 being a line this cannot reach")
	harness.Equal(t, found.troubles(), "", "what could not be read")

	ordinary := newBox(t)
	ordinary.clean()
	ordinary.says(miseLsHome, `{"node":[`+miseEntry("24.22.1", "/installs/node/24.22.1", true)+`]}`)
	ordinary.serves(nodeIndexURL, `[{"version":"v24.23.0","date":"2026-10-28","security":false}]`)

	harness.Equal(t, len(ordinary.look().Exposures), 0,
		"exposures from a release node does not call a security release")
}

func TestAnIndexThatCouldNotBeReadIsSaidRatherThanTakenAsNoSecurityRelease(t *testing.T) {
	t.Run("nothing said which node is active", func(t *testing.T) {
		b := newBox(t)
		b.clean()

		found := b.look()

		harness.Equal(t, len(b.asked("nodejs.org")), 0, "requests for the index")
		harness.Equal(t, found.troubles(), "", "what could not be read")
	})

	t.Run("it did not answer", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.says(miseLsHome, `{"node":[`+miseEntry("24.22.0", "/installs/node/24.22.0", true)+`]}`)
		b.refuses(nodeIndexURL, "it answered 503: Service Unavailable")

		harness.Wants(t, b.look().troubles(),
			"the node release index could not be read: it answered 503: Service Unavailable")
	})

	t.Run("it answered something else", func(t *testing.T) {
		b := newBox(t)
		b.clean()
		b.says(miseLsHome, `{"node":[`+miseEntry("24.22.0", "/installs/node/24.22.0", true)+`]}`)
		b.serves(nodeIndexURL, "<html>maintenance</html>")

		harness.Wants(t, b.look().troubles(), "the node release index answered something that is not JSON")
	})
}
