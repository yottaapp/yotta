package registryclient

import (
	"context"
	"errors"
	"net/url"
	"strconv"
)

type WorkflowSales struct {
	PriceCents int64  `json:"priceCents"`
	Currency   string `json:"currency"`
	Available  bool   `json:"available"`
	Revision   int64  `json:"revision"`
}

type WorkflowSubmission struct {
	SubmissionID string          `json:"submissionId"`
	WorkflowID   string          `json:"workflowId"`
	Status       string          `json:"status"`
	Reason       string          `json:"reason"`
	SubmittedAt  string          `json:"submittedAt"`
	Release      WorkflowRelease `json:"release"`
	Sales        WorkflowSales   `json:"sales"`
}

type WorkflowSubmissionPage struct {
	Items []WorkflowSubmission `json:"items"`
	Total int                  `json:"total"`
	Page  int                  `json:"page"`
	Size  int                  `json:"size"`
}

func (c *Client) WorkflowSubmissions(ctx context.Context, page int) (WorkflowSubmissionPage, error) {
	var out WorkflowSubmissionPage
	err := c.checkoutRequest(ctx, "GET", "/v1/creator/workflow-submissions?size=100&page="+strconv.Itoa(page), nil, &out)
	return out, err
}

func (c *Client) WorkflowSales(ctx context.Context, id string) (*WorkflowSales, error) {
	var out WorkflowSales
	err := c.checkoutRequest(ctx, "GET", "/v1/workflows/"+url.PathEscape(id)+"/sales", nil, &out)
	var problem Problem
	if errors.As(err, &problem) && problem.Status == 404 && problem.Code == "registry.management.not_found" {
		return nil, nil
	}
	return &out, err
}
