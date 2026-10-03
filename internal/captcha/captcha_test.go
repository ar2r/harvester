package captcha

import (
	"context"
	"errors"
	"testing"

	"github.com/jfk9w-go/based"
	"github.com/jfk9w-go/rucaptcha-api"
)

func TestNewTokenProviderDisabledWithoutKey(t *testing.T) {
	// Без ключа rucaptcha капча выключена: провайдер nil, ошибки нет —
	// авторизация ФНС просто не будет его использовать.
	provider, err := NewTokenProvider(&Config{}, based.StandardClock)
	if err != nil {
		t.Fatal(err)
	}

	if provider != nil {
		t.Fatalf("expected nil provider without key, got %T", provider)
	}
}

func TestNewTokenProviderCreatesClientWithKey(t *testing.T) {
	provider, err := NewTokenProvider(&Config{RucaptchaKey: "test-key"}, based.StandardClock)
	if err != nil {
		t.Fatal(err)
	}

	if provider == nil {
		t.Fatal("expected provider with non-empty key")
	}
}

func TestTokenProviderPassesCaptchaParamsAndReturnsAnswer(t *testing.T) {
	var gotIn rucaptcha.SolveIn

	provider := &rucaptchaTokenProvider{client: fakeRucaptchaClient{
		solve: func(in rucaptcha.SolveIn) (*rucaptcha.SolveOut, error) {
			gotIn = in
			return &rucaptcha.SolveOut{Answer: "solved-token"}, nil
		},
	}}

	token, err := provider.GetCaptchaToken(context.Background(), "agent/1.0", "site-key", "https://example.ru")
	if err != nil {
		t.Fatal(err)
	}

	if token != "solved-token" {
		t.Fatalf("expected solved token, got %q", token)
	}

	in, ok := gotIn.(*rucaptcha.YandexSmartCaptchaIn)
	if !ok {
		t.Fatalf("expected YandexSmartCaptchaIn, got %T", gotIn)
	}

	if in.UserAgent != "agent/1.0" || in.SiteKey != "site-key" || in.PageURL != "https://example.ru" {
		t.Fatalf("unexpected captcha params: %+v", in)
	}
}

func TestTokenProviderPropagatesSolveError(t *testing.T) {
	provider := &rucaptchaTokenProvider{client: fakeRucaptchaClient{
		solve: func(rucaptcha.SolveIn) (*rucaptcha.SolveOut, error) {
			return nil, errors.New("balance is depleted")
		},
	}}

	if _, err := provider.GetCaptchaToken(context.Background(), "", "", ""); err == nil {
		t.Fatal("expected solve error to propagate")
	}
}

type fakeRucaptchaClient struct {
	solve func(in rucaptcha.SolveIn) (*rucaptcha.SolveOut, error)
}

func (f fakeRucaptchaClient) Solve(_ context.Context, in rucaptcha.SolveIn) (*rucaptcha.SolveOut, error) {
	return f.solve(in)
}
