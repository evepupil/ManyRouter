package httptransport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/evepupil/ManyRouter/internal/domain/operations"
	"github.com/gin-gonic/gin"
)

func TestOperationsRequireExplicitStateFields(t *testing.T) {
	cases := []struct {
		name, kind, body string
		target           any
	}{
		{"missing visible", "relation", `{"version":1,"group_display_name":"A","desired_status":"enabled","resume":false,"reason":"edit"}`, &operations.RelationInput{}},
		{"null visible", "relation", `{"version":1,"group_display_name":"A","visible":null,"desired_status":"enabled","resume":false,"reason":"edit"}`, &operations.RelationInput{}},
		{"missing strategy enabled", "strategy", `{"version":0,"visible":false,"display_name":"A","member_relation_ids":[],"reason":"edit"}`, &operations.StrategyInput{}},
		{"missing model enabled", "create_supplier", `{"code":"s","name":"S","upstream_base_url":"https://s.test","upstream_api_key":"secret-key","models":[{"model":"a","upstream_model":"a","input_price":"1","output_price":"2","currency":"USD"}]}`, &operations.SupplierInput{}},
		{"missing deployment visible", "deploy", `{"supplier_id":"00000000-0000-0000-0000-000000000001","reason":"deploy","sites":[{"site_id":"00000000-0000-0000-0000-000000000002","group_display_name":"A","sale_ratio":"1"}]}`, &operations.DeploymentInput{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			if decodeOperationJSON(c, test.kind, test.target) || response.Code != http.StatusBadRequest {
				t.Fatal("omitted state accepted as false")
			}
		})
	}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"version":1,"group_display_name":"A","visible":false,"desired_status":"disabled","resume":false,"reason":"edit"}`))
	var input operations.RelationInput
	if !decodeOperationJSON(c, "relation", &input) || input.Visible || input.Resume {
		t.Fatal("explicit false state rejected or changed")
	}
}
