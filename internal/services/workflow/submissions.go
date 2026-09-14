package workflow

import (
	"context"
	"errors"
	"github.com/yottaapp/yotta/internal/registryclient"
)

type RegistryPublicationHistory struct {
	Items []registryclient.WorkflowSubmission `json:"items"`
	Sales *registryclient.WorkflowSales       `json:"sales,omitempty"`
}

// RegistryWorkflowPublicationHistory includes private candidates omitted by the public catalog.
func (s *Service) RegistryWorkflowPublicationHistory(ctx context.Context, workflowID string) (RegistryPublicationHistory, error) {
	out := RegistryPublicationHistory{Items: []registryclient.WorkflowSubmission{}}
	c, ok := s.registry.(interface {
		WorkflowSubmissions(context.Context, int) (registryclient.WorkflowSubmissionPage, error)
		WorkflowSales(context.Context, string) (*registryclient.WorkflowSales, error)
	})
	if !ok {
		return out, unavailable("registry")
	}
	for page := 1; ; page++ {
		result, err := c.WorkflowSubmissions(ctx, page)
		if err != nil {
			return out, registryError("submissions", err)
		}
		for _, item := range result.Items {
			if item.WorkflowID == workflowID {
				out.Items = append(out.Items, item)
			}
		}
		if page*100 >= result.Total {
			break
		}
		if len(result.Items) == 0 || page >= 100000 {
			return out, registryError("submissions", errors.New("invalid submission pagination"))
		}
	}
	var err error
	out.Sales, err = c.WorkflowSales(ctx, workflowID)
	return out, registryErrorOrNil(err)
}

func registryErrorOrNil(err error) error {
	if err == nil {
		return nil
	}
	return registryError("sales", err)
}

// OpenMySubmissions opens the creator's submissions in the configured Hub.
func (s *Service) OpenMySubmissions() error {
	browser := s.walletPageBrowser
	if browser == nil && s.wallet != nil {
		browser = s.wallet.browser
	}
	if s.community == nil || browser == nil {
		return unavailable("submissions")
	}
	return accountError(browser.OpenURL(s.community.SubmissionsURL()))
}
