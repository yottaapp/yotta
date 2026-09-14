package workflow

import (
	"context"
	"errors"
	"github.com/yottaapp/yotta/internal/apperr"
	"testing"
)

func TestOpenWalletPageDoesNotRequireRegistryPaymentAPI(t *testing.T) {
	browser := &submissionsBrowser{}
	service := &Service{}
	WithWalletPage(browser, "https://pay.yuelili.com/wallet")(service)
	if err := service.OpenRegistryWallet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if browser.opened != "https://pay.yuelili.com/wallet" {
		t.Fatal(browser.opened)
	}
	browser.err = errors.New("browser failed")
	if apperr.From(service.OpenRegistryWallet(context.Background())).ID != "workflow.account.unavailable" {
		t.Fatal("browser failure lost structured error")
	}
}
