package reconciliation_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/evepupil/ManyRouter/internal/adapters/gateway/newapi"
	"github.com/evepupil/ManyRouter/internal/application/reconciliation"
	"github.com/google/uuid"
)

type echoedCredentialGateway struct {
	*siteTestGateway
	client *newapi.Client
}

func (gateway echoedCredentialGateway) TestChannel(ctx context.Context, id int64, model string, secret []byte) error {
	return gateway.client.TestChannel(ctx, id, model, secret)
}

func TestSiteUpstreamFailureDoesNotPersistSupplierCredential(t *testing.T) {
	t.Parallel()
	store, gateway, _ := siteFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "provider rejected supplier-secret"})
	}))
	defer server.Close()
	client, err := newapi.NewClient(server.URL, []byte("admin-example"), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	service, err := reconciliation.NewService(store, fakeVault{}, siteTestFactory{gateway: echoedCredentialGateway{siteTestGateway: gateway, client: client}}, nil, time.Now, uuid.New)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Run(context.Background(), store.bundle.Operation.ID); err != nil {
		t.Fatal(err)
	}
	if store.failure == nil {
		t.Fatal("upstream rejection was not recorded")
	}
	if strings.Contains(store.failure.Message, "supplier-secret") {
		t.Fatal("upstream credential was persisted in the operation failure")
	}
	for _, step := range store.steps {
		if strings.Contains(step.ErrorMessage, "supplier-secret") {
			t.Fatal("upstream credential was persisted in a synchronization step")
		}
	}
}
