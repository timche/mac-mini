package vuln

import (
	"encoding/json"
	"strings"

	"github.com/timche/mac-mini/hachiko/internal/update"
)

// node's own release index, which is the only place node says a release was a security
// release: it publishes no advisory feed and no GitHub security advisories, and the CVEs
// behind one of its releases land in NVD days later and under OpenSSL's or libuv's name as
// often as node's. The index is the authoritative answer and it is one file.
const (
	nodeIndexURL = "https://nodejs.org/dist/index.json"
	sourceNode   = "the node release index"
)

type nodeRelease struct {
	Version  string `json:"version"`
	Date     string `json:"date"`
	Security bool   `json:"security"`
}

// Whether a security release has come out above the node mise has, in the same major line.
// The same line and no further, because that is the whole of what an upgrade here can reach:
// the global config requests `lts`, so `mise up` takes the newest release inside the line and
// never crosses a major — a fix that is only in a newer major is a decision about which node
// this Mac runs, which is nobody's to make at four in the morning.
func (c checker) node(inv Inventory, found *Found) {
	installed := inv.version(fromMise, "node")
	if installed == "" {
		return
	}

	body, err := c.deps.Get(nodeIndexURL)
	if err != nil {
		found.Troubles = append(found.Troubles, sourceNode+" could not be read: "+oneLine(err.Error()))
		return
	}

	var releases []nodeRelease
	if json.Unmarshal(body, &releases) != nil {
		found.Troubles = append(found.Troubles, sourceNode+" answered something that is not JSON")
		return
	}

	found.read(sourceNode)

	line := major(installed)
	newest, when := "", ""

	for _, release := range releases {
		version := strings.TrimPrefix(release.Version, "v")

		if !release.Security || major(version) != line || compare(version, installed) <= 0 {
			continue
		}
		if newest == "" || compare(version, newest) > 0 {
			newest, when = version, release.Date
		}
	}

	if newest == "" {
		return
	}

	found.Exposures = append(found.Exposures, Exposure{
		// node names no advisory of its own, so the release carrying the fix is the identity:
		// it is what the index says, what a person would look up, and the same string on every
		// check until this Mac is on it or past it.
		ID:      "node-v" + newest,
		Source:  sourceNode,
		Summary: "node released " + newest + " as a security release on " + when,
		Name:    "node",
		Version: installed,
		Fixed:   newest,
		Target:  update.MiseTarget("node"),
	})
}

// The major of a version, which is the line a release belongs to.
func major(version string) string {
	line, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	return line
}
