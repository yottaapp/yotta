package workflow

import (
	"context"
	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/registryclient"
	"testing"
)

func TestPublicationKeyIncludesSalesAndPresentation(t *testing.T) {
	request := PublishRegistryRequest{WorkflowID: "work", ReleaseVersion: "1.0.0", Title: "title", Sales: &registryclient.WorkflowSales{PriceCents: 100}}
	key := publicationKey(request, "source")
	if key != publicationKey(request, "source") {
		t.Fatal("retry key changed")
	}
	request.Sales.PriceCents = 200
	if key == publicationKey(request, "source") {
		t.Fatal("price change reused key")
	}
	key = publicationKey(request, "source")
	request.Summary = "new summary"
	if key == publicationKey(request, "source") {
		t.Fatal("presentation change reused key")
	}
	key = publicationKey(request, "source")
	request.ResubmissionID = "rejected-candidate"
	if key == publicationKey(request, "source") {
		t.Fatal("intentional resubmission reused rejected receipt")
	}
}

func TestPublicationProjectsReviewAndValidatesSales(t *testing.T) {
	view := registryReleaseView(registryclient.WorkflowRelease{PublicationStatus: "pending_review", SubmissionID: "candidate", Sales: &registryclient.WorkflowSales{PriceCents: 100}})
	if view.PublicationStatus != "pending_review" || view.SubmissionID != "candidate" || view.Sales.PriceCents != 100 {
		t.Fatalf("view = %#v", view)
	}
	s := &Service{}
	_, err := s.PublishSourceToRegistry(context.Background(), PublishRegistryRequest{Sales: &registryclient.WorkflowSales{PriceCents: -1}})
	if apperr.From(err).ID != "workflow.registry.invalid_sales" {
		t.Fatalf("err = %v", err)
	}
	_, err = s.PublishSourceToRegistry(context.Background(), PublishRegistryRequest{
		Sales:          &registryclient.WorkflowSales{PriceCents: 100, Currency: "CNY", Available: true},
		ReleaseVersion: "1.0.0",
		Title:          "Paid workflow",
		Summary:        "Paid workflow summary",
	})
	if apperr.From(err).ID == "workflow.registry.invalid_sales" {
		t.Fatalf("paid sales unexpectedly require author terms: %v", err)
	}
	for _, code := range []string{"submission_pending", "idempotency_conflict", "review_required"} {
		err := registryError("publish", registryclient.Problem{Code: "registry." + code})
		if apperr.From(err).ID != "workflow.registry."+code {
			t.Fatalf("err = %v", err)
		}
	}
}

type publicationHistoryClient struct {
	RegistryClient
	pages []int
}

func (c *publicationHistoryClient) WorkflowSubmissions(_ context.Context, page int) (registryclient.WorkflowSubmissionPage, error) {
	c.pages = append(c.pages, page)
	id := "other"
	if page == 2 {
		id = "work"
	}
	return registryclient.WorkflowSubmissionPage{Total: 101, Page: page, Size: 100, Items: []registryclient.WorkflowSubmission{{WorkflowID: id, Status: "rejected", Reason: "clarify terms"}}}, nil
}
func (c *publicationHistoryClient) WorkflowSales(_ context.Context, id string) (*registryclient.WorkflowSales, error) {
	return &registryclient.WorkflowSales{Revision: 7}, nil
}
func TestPublicationHistoryIncludesPrivateCandidateOnLaterPage(t *testing.T) {
	c := &publicationHistoryClient{}
	s := &Service{registry: c}
	result, err := s.RegistryWorkflowPublicationHistory(context.Background(), "work")
	if err != nil || len(c.pages) != 2 || len(result.Items) != 1 || result.Items[0].Reason != "clarify terms" || result.Sales.Revision != 7 {
		t.Fatalf("history = %#v, pages = %v, err = %v", result, c.pages, err)
	}
}
