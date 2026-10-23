package daemon

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestProposalBacklogItems_NeedsTheProposalsDocsTab(t *testing.T) {
	_, cs, data := startPushSession(t, "backlogrequest")

	code, env := postProposalRoute(t, cs, "RequestProposalBacklogItems", RequestProposalBacklogItemsRequest{SessionID: data.ID})
	require.Equal(t, http.StatusInternalServerError, code)
	require.Contains(t, env["error"].(map[string]any)["message"], "slug is required")

	code, env = postProposalRoute(t, cs, "RequestProposalBacklogItems", RequestProposalBacklogItemsRequest{SessionID: data.ID, Slug: "other"})
	require.Equal(t, http.StatusInternalServerError, code)
	require.Contains(t, env["error"].(map[string]any)["message"], `no Docs tab for proposal "other"`)

	code, env = postProposalRoute(t, cs, "RequestProposalBacklogItems", RequestProposalBacklogItemsRequest{SessionID: data.ID, Slug: "p"})
	require.Equal(t, http.StatusOK, code, env)
}
