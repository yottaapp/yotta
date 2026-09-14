package registryclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPaidPublicationAndCreatorSubmissionContract(t *testing.T) {
	sales := WorkflowSales{PriceCents: 1234, Currency: "CNY", Available: true, Revision: 0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer user-token" {
			t.Error("missing creator token")
		}
		if r.Method == "POST" {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			var got WorkflowSales
			if err := json.Unmarshal([]byte(r.FormValue("sales")), &got); err != nil || got != sales {
				t.Errorf("sales = %#v, %v", got, err)
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"workflowId":"work","releaseVersion":"1.0.0","publicationStatus":"pending_review","submissionId":"candidate"}`))
		} else {
			if r.URL.Path != "/v1/creator/workflow-submissions" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "100" {
				t.Errorf("URL = %s", r.URL)
			}
			_, _ = w.Write([]byte(`{"items":[{"submissionId":"candidate","workflowId":"work","status":"rejected","reason":"Please clarify usage","sales":{"priceCents":1234,"currency":"CNY"},"release":{"workflowId":"work","releaseVersion":"1.0.0"}}],"total":101,"page":2,"size":100}`))
		}
	}))
	defer server.Close()
	client := mustClient(t, server.URL, staticToken("user-token"))
	got, err := client.PublishWorkflow(context.Background(), PublishRequest{Sales: &sales, Bundle: strings.NewReader("bundle"), IdempotencyKey: "paid-publication-key"})
	if err != nil || got.PublicationStatus != "pending_review" || got.SubmissionID != "candidate" {
		t.Fatalf("result = %#v, %v", got, err)
	}
	page, err := client.WorkflowSubmissions(context.Background(), 2)
	if err != nil || page.Total != 101 || len(page.Items) != 1 || page.Items[0].Status != "rejected" || page.Items[0].Reason != "Please clarify usage" || page.Items[0].Sales.PriceCents != 1234 {
		t.Fatalf("page = %#v, %v", page, err)
	}
}
