package automerge_test

import (
	"testing"

	"github.com/joeybrown/automerge-go"
	"github.com/stretchr/testify/require"
)

func TestDocClose(t *testing.T) {
	d := automerge.New()
	require.NoError(t, d.RootMap().Set("x", "bloop"))
	_, err := d.Commit("boop")
	require.NoError(t, err)

	require.NoError(t, d.Close())
	require.NoError(t, d.Close(), "Close must be idempotent")
}

func TestDocCloseDoesNotAffectOtherDocs(t *testing.T) {
	d1 := automerge.New()
	d2 := automerge.New()
	defer d2.Close()

	require.NoError(t, d1.Close())

	require.NoError(t, d2.RootMap().Set("x", "still works"))
	_, err := d2.Commit("after other doc closed")
	require.NoError(t, err)

	d3 := automerge.New()
	defer d3.Close()
	require.NoError(t, d3.RootMap().Set("y", "new docs still work"))
}
