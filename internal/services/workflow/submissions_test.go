package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/communityclient"
)

type submissionsBrowser struct {
	opened string
	err    error
}

func (b *submissionsBrowser) OpenURL(raw string) error { b.opened = raw; return b.err }

func TestOpenMySubmissionsUsesConfiguredHub(t *testing.T) {
	for _, base := range []string{"https://hub.example", "http://127.0.0.1:18432", "https://yotta.yuelili.com/api/hub"} {
		t.Run(base, func(t *testing.T) {
			client, err := communityclient.New(base, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			browser := &submissionsBrowser{}
			s := &Service{community: client}
			WithWalletPage(browser, "https://pay.yuelili.com/wallet")(s)
			if err := s.OpenMySubmissions(); err != nil {
				t.Fatal(err)
			}
			if browser.opened != strings.Split(base, "/api/")[0]+"/creator/workflows" {
				t.Fatalf("opened %q", browser.opened)
			}
			browser.err = errors.New("browser unavailable")
			if got := apperr.From(s.OpenMySubmissions()).ID; got != "workflow.account.unavailable" {
				t.Fatalf("problem = %s", got)
			}
		})
	}
}
