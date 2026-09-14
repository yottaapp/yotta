package communityclient

import "net/url"

// SubmissionsURL uses the same configured Hub as community requests.
func (c *Client) SubmissionsURL() string {
	u, _ := url.Parse(c.base)
	return u.ResolveReference(&url.URL{Path: "/creator/workflows"}).String()
}
