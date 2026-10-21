module github.com/timche/mac-mini/mochi

go 1.27

require github.com/timche/mac-mini/discord v0.0.0

// The Discord client is this repository's own, built from the checkout beside mochi: nothing
// is fetched, and go.sum has nothing to pin.
replace github.com/timche/mac-mini/discord => ../discord
