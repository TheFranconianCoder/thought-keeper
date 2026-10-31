package usecases

import (
	"context"
	"io"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// nodeTexts fills each node's Text from its REFERENCE — the byte range in the source's readable text.
//
// The outline stores structure and references, never the text: the text is already there, in the capture
// rendering (or in the object itself, when its bytes are readable as they are), and an outline that
// carries it is a second copy of the whole document. Reading it here, once for a whole tree, is what
// keeps a source one document instead of two.
//
// A node that already carries its text (an outline written before the references existed) is left
// alone: the file is the authority for what it holds.
func nodeTexts(ctx context.Context, index interfaces.IndexRepository, store interfaces.SourceStore, hash string, nodes []entities.SourceNode) ([]entities.SourceNode, error) {
	if index == nil || store == nil {
		return nodes, nil
	}
	needs := false
	for _, n := range nodes {
		if n.Text == "" && n.TextEnd > n.TextStart {
			needs = true
			break
		}
	}
	if !needs {
		return nodes, nil
	}
	src, err := index.GetSourceByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	text, err := readableText(ctx, store, src)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		n := &nodes[i]
		if n.Text != "" || n.TextEnd <= n.TextStart {
			continue
		}
		// A range past the end means the readable text is not the one the outline was built from. The
		// reconciler invalidates that derivation; here the node simply keeps what it has.
		if n.TextStart < 0 || n.TextEnd > len(text) {
			continue
		}
		n.Text = text[n.TextStart:n.TextEnd]
	}
	return nodes, nil
}

// readableText is the source's readable text: the capture rendering for a kind whose bytes are not
// readable as they are (a PDF, an EPUB), the object itself otherwise — exactly the text step 2 parses,
// which is what makes the outline's references point at something stable.
func readableText(ctx context.Context, store interfaces.SourceStore, src entities.Source) (string, error) {
	_ = ctx
	var (
		rc  io.ReadCloser
		err error
	)
	if src.NeedsCapture() {
		rc, err = store.OpenRendered(src.Hash, entities.SidecarRender)
	} else {
		rc, err = store.Open(src.Hash, src.Ext)
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
