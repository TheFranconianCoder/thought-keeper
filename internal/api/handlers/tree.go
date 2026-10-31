package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/TheFranconianCoder/thought-keeper/internal/api/payloads"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/entities"
	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases"
	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// TreeHandler serves the tree endpoints.
type TreeHandler struct {
	Tree      usecases.TreeService
	Node      usecases.NodeService
	Rebuilder usecases.RebuildTreesService
	Builder   usecases.TreeBuilder
	Mark      MaintenanceMarker
}

// Get serves GET /api/v1/sources/{hash}/tree.
func (h TreeHandler) Get(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	nodes, err := h.Tree(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.Tree{
		Hash:  hash,
		Nodes: payloads.NodesFromPartials(nodes),
		Count: len(nodes),
	})
}

// GetNode serves GET /api/v1/sources/{hash}/nodes/{node}.
func (h TreeHandler) GetNode(w http.ResponseWriter, r *http.Request) {
	node, err := h.Node(r.Context(), chi.URLParam(r, "hash"), chi.URLParam(r, "node"))
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.NodeFromPartial(node))
}

// RebuildOne serves POST /api/v1/sources/{hash}/rebuild-tree: rebuild the tree of one file.
func (h TreeHandler) RebuildOne(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !entities.IsSHA256Hex(hash) {
		payloads.WriteError(w, types.New("invalid_hash", "hash must be a 64-character hex SHA-256", types.ErrInvalidHash))
		return
	}
	if h.Builder == nil {
		payloads.WriteError(w, types.New("not_implemented", "tree rebuilding is unavailable", types.ErrNotImplemented))
		return
	}
	res, err := h.Builder(r.Context(), hash)
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.TreeRebuilt{
		Hash: res.Hash, Nodes: res.Nodes, Pages: res.Pages, Title: res.Title, OCR: res.OCR,
	})
}

func (h TreeHandler) Rebuild(w http.ResponseWriter, r *http.Request) {
	if h.Mark != nil {
		defer h.Mark.MarkMaintenance("rebuild-trees", "")()
	}
	res, err := h.Rebuilder(r.Context())
	if err != nil {
		payloads.WriteError(w, err)
		return
	}
	payloads.WriteJSON(w, http.StatusOK, payloads.RebuildTreesResult{
		Sources: res.Sources,
		Built:   res.Built,
		Failed:  res.Failed,
	})
}
