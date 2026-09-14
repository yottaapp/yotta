package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/yottaapp/yotta/internal/apperr"
	"github.com/yottaapp/yotta/internal/nativeoidc"
	"github.com/yottaapp/yotta/internal/registryclient"
	"github.com/yottaapp/yotta/internal/securestore"
	"net/url"
	"sync"
	"time"
)

type walletClient interface {
	RequestWalletAuthorization(context.Context, registryclient.WalletAuthorizationRequest) (registryclient.WalletAuthorizationResult, error)
	ExchangeWalletAuthorization(context.Context, string, string) (registryclient.WalletAuthorizationResult, error)
	WalletBalance(context.Context, string) (registryclient.WalletBalance, error)
	PayWallet(context.Context, string, registryclient.WalletPayment) (registryclient.WalletPaid, error)
}
type walletSession struct {
	store   securestore.Store
	scope   string
	browser nativeoidc.Browser
	mu      sync.Mutex
	cancel  context.CancelFunc
}
type walletSaved struct {
	Token     string
	ID        string
	Verifier  string
	Key       string
	ExpiresAt string
}
type WalletView struct {
	Authorized   bool   `json:"authorized"`
	Currency     string `json:"currency"`
	BalanceCents int64  `json:"balanceCents"`
}

func WithWallet(store securestore.Store, scope string, browser nativeoidc.Browser) Option {
	return func(s *Service) { s.wallet = &walletSession{store: store, scope: scope, browser: browser} }
}
func (w *walletSession) key(user string) string {
	sum := sha256.Sum256([]byte(w.scope + "\x00" + user))
	return "Yotta/wallet/" + hex.EncodeToString(sum[:])
}
func (w *walletSession) read(user string) (walletSaved, error) {
	var v walletSaved
	raw, e := w.store.Get(w.key(user))
	if errors.Is(e, securestore.ErrNotFound) {
		return v, nil
	}
	if e != nil {
		return v, e
	}
	e = json.Unmarshal([]byte(raw), &v)
	return v, e
}
func (w *walletSession) save(user string, v walletSaved) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return w.store.Set(w.key(user), string(raw))
}
func (s *Service) walletContext(ctx context.Context) (walletClient, string, error) {
	c, ok := s.registry.(walletClient)
	if !ok || s.wallet == nil || s.account == nil {
		return nil, "", unavailable("wallet")
	}
	if _, e := s.account.Token(ctx); e != nil {
		return nil, "", registryError("wallet", e)
	}
	user := s.account.Profile().UserKey
	if user == "" {
		return nil, "", registryError("wallet", nativeoidc.ErrAuthenticationRequired)
	}
	return c, user, nil
}
func walletError(e error) error {
	if e == nil {
		return nil
	}
	var p registryclient.Problem
	if errors.As(e, &p) {
		if p.Code == "commerce.wallet_insufficient" {
			return projectError("workflow.wallet.insufficient", apperr.CategoryDomain, nil, false, e)
		}
		if p.Status == 403 {
			return projectError("workflow.wallet.authorization_required", apperr.CategoryPolicy, nil, false, e)
		}
	}
	if errors.Is(e, securestore.ErrUnavailable) {
		return projectError("workflow.wallet.storage_unavailable", apperr.CategoryInfrastructure, nil, true, e)
	}
	return registryError("wallet", e)
}
func (s *Service) RegistryWallet(ctx context.Context) (WalletView, error) {
	c, user, e := s.walletContext(ctx)
	if e != nil {
		return WalletView{}, e
	}
	saved, e := s.wallet.read(user)
	if e != nil {
		return WalletView{}, walletError(e)
	}
	if saved.Token == "" {
		return WalletView{}, nil
	}
	b, e := c.WalletBalance(ctx, saved.Token)
	var p registryclient.Problem
	if errors.As(e, &p) && p.Status == 403 {
		return WalletView{}, nil
	}
	if e != nil {
		return WalletView{}, walletError(e)
	}
	return WalletView{true, b.Currency, b.BalanceCents}, nil
}
func (s *Service) CancelRegistryWalletAuthorization() {
	if s.wallet != nil {
		s.wallet.mu.Lock()
		defer s.wallet.mu.Unlock()
		if s.wallet.cancel != nil {
			s.wallet.cancel()
		}
	}
}
func (s *Service) AuthorizeRegistryWallet(ctx context.Context) (WalletView, error) {
	c, user, e := s.walletContext(ctx)
	if e != nil {
		return WalletView{}, e
	}
	w := s.wallet
	w.mu.Lock()
	if w.cancel != nil {
		w.mu.Unlock()
		return WalletView{}, registryError("wallet", context.Canceled)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	w.cancel = cancel
	w.mu.Unlock()
	defer func() { cancel(); w.mu.Lock(); w.cancel = nil; w.mu.Unlock() }()
	current := func() bool { return s.account.Profile().UserKey == user }
	saved, e := w.read(user)
	if e != nil {
		return WalletView{}, walletError(e)
	}
	expiry, _ := time.Parse(time.RFC3339, saved.ExpiresAt)
	if saved.Verifier == "" || !expiry.After(time.Now()) {
		random := make([]byte, 32)
		if _, e = rand.Read(random); e != nil {
			return WalletView{}, walletError(e)
		}
		verifier := base64.RawURLEncoding.EncodeToString(random)
		if _, e = rand.Read(random); e != nil {
			return WalletView{}, walletError(e)
		}
		saved = walletSaved{Verifier: verifier, Key: base64.RawURLEncoding.EncodeToString(random), ExpiresAt: time.Now().Add(10 * time.Minute).Format(time.RFC3339)}
		if e = w.save(user, saved); e != nil {
			return WalletView{}, projectError("workflow.wallet.storage_unavailable", apperr.CategoryInfrastructure, nil, true, e)
		}
	}
	challenge := sha256.Sum256([]byte(saved.Verifier))
	out, e := c.RequestWalletAuthorization(ctx, registryclient.WalletAuthorizationRequest{Challenge: base64.RawURLEncoding.EncodeToString(challenge[:]), IdempotencyKey: saved.Key})
	if e != nil {
		return WalletView{}, walletError(e)
	}
	if !current() {
		return WalletView{}, registryError("wallet", context.Canceled)
	}
	u, e := url.Parse(out.AuthorizeURL)
	if e != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return WalletView{}, unavailable("wallet")
	}
	saved.ID = out.Authorization.ID
	saved.ExpiresAt = out.Authorization.ExpiresAt
	if e = w.save(user, saved); e != nil {
		return WalletView{}, projectError("workflow.wallet.storage_unavailable", apperr.CategoryInfrastructure, nil, true, e)
	}
	if e = w.browser.OpenURL(out.AuthorizeURL); e != nil {
		return WalletView{}, walletError(e)
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return WalletView{}, walletError(ctx.Err())
		case <-ticker.C:
			if !current() {
				return WalletView{}, registryError("wallet", context.Canceled)
			}
			out, e = c.ExchangeWalletAuthorization(ctx, saved.ID, saved.Verifier)
			if e != nil {
				return WalletView{}, walletError(e)
			}
			if !current() {
				return WalletView{}, registryError("wallet", context.Canceled)
			}
			if out.Token == "" {
				continue
			}
			if e = w.save(user, walletSaved{Token: out.Token, ExpiresAt: out.Authorization.ExpiresAt}); e != nil {
				return WalletView{}, projectError("workflow.wallet.storage_unavailable", apperr.CategoryInfrastructure, nil, true, e)
			}
			return s.RegistryWallet(ctx)
		}
	}
}
func (s *Service) PayRegistryWallet(ctx context.Context, input registryclient.WalletPayment) (registryclient.WalletPaid, error) {
	c, user, e := s.walletContext(ctx)
	if e != nil {
		return registryclient.WalletPaid{}, e
	}
	saved, e := s.wallet.read(user)
	if e != nil {
		return registryclient.WalletPaid{}, walletError(e)
	}
	if saved.Token == "" {
		return registryclient.WalletPaid{}, projectError("workflow.wallet.authorization_required", apperr.CategoryPolicy, nil, false, nil)
	}
	out, e := c.PayWallet(ctx, saved.Token, input)
	return out, walletError(e)
}

// WithWalletPage separates opening the website from wallet payment authorization.
func WithWalletPage(browser nativeoidc.Browser, raw string) Option {
	return func(s *Service) { s.walletPageBrowser = browser; s.walletPageURL = raw }
}

func (s *Service) OpenRegistryWallet(ctx context.Context) error {
	if s.walletPageURL != "" && s.walletPageBrowser != nil {
		u, err := url.Parse(s.walletPageURL)
		if err != nil || u.Host == "" || u.User != nil || u.Scheme != "https" {
			return unavailable("wallet")
		}
		return accountError(s.walletPageBrowser.OpenURL(u.String()))
	}
	if s.wallet == nil {
		return unavailable("wallet")
	}
	c, ok := s.registry.(interface {
		WalletURL(context.Context) (string, error)
	})
	if !ok {
		return unavailable("wallet")
	}
	raw, e := c.WalletURL(ctx)
	if e != nil {
		return walletError(e)
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return unavailable("wallet")
	}
	return walletError(s.wallet.browser.OpenURL(u.String()))
}
