// Package mocklkdr эмулирует неофициальное API ЛК ФНС «Мои чеки онлайн»
// (https://mco.nalog.ru/api) с детерминированным набором данных.
//
// Используется двумя способами:
//   - в интеграционных тестах как http.Handler для httptest.Server
//     (запросы клиента перенаправляются на него через http.RoundTripper);
//   - как отдельный сервис (cmd/mocklkdr, docker compose up mocklkdr),
//     к которому приложение подключается через lkdr.apiUrl.
package mocklkdr

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/lkdr-api"
)

const (
	// AccessToken — токен доступа, который мок выдаёт при авторизации
	// и обновлении. Тесты могут сверять с ним заголовок Authorization.
	AccessToken = "mock-access-token"

	// RefreshToken — рефреш-токен, выдаваемый моком.
	RefreshToken = "mock-refresh-token"

	// ChallengeToken — токен SMS-челенджа из /v2/auth/challenge/sms/start.
	ChallengeToken = "mock-challenge-token"
)

// Request — запись о входящем запросе для проверок в тестах.
type Request struct {
	Path  string
	Body  string
	Token string // значение Authorization без префикса "Bearer "
}

// Server — мок API. Безопасен для конкурентного использования.
type Server struct {
	mu       sync.Mutex
	requests []Request
	now      func() time.Time
}

func New() *Server {
	return &Server{now: time.Now}
}

// Requests возвращает копию журнала входящих запросов.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()

	requests := make([]Request, len(s.requests))
	copy(requests, s.requests)
	return requests
}

// Reset очищает журнал запросов.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Реальный base URL — https://mco.nalog.ru/api, поэтому пути запросов
	// клиента начинаются с /api (redirect-транспорт сохраняет путь целиком).
	mux.HandleFunc("/api/v2/auth/challenge/sms/start", s.handleStart)
	mux.HandleFunc("/api/v1/auth/challenge/sms/verify", s.handleVerify)
	mux.HandleFunc("/api/v1/auth/token", s.handleRefresh)
	mux.HandleFunc("/api/v1/receipt", s.handleReceipt)
	mux.HandleFunc("/api/v1/receipt/fiscal_data", s.handleFiscalData)
	return mux
}

func (s *Server) record(r *http.Request) []byte {
	body, _ := io.ReadAll(r.Body)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{
		Path:  r.URL.Path,
		Body:  string(body),
		Token: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
	})

	return body
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}

func writeError(w http.ResponseWriter, status int, code lkdr.ErrorCode, message string) {
	writeJSON(w, status, lkdr.Error{Code: code, Message: message})
}

func (s *Server) tokens() lkdr.Tokens {
	// Сроки строим в UTC: DateTimeTZ маршалится wall-clock временем с
	// литерой "Z" и парсится как UTC, поэтому локальная зона процесса
	// сдвигала бы выданные сроки.
	now := s.now().UTC()
	return lkdr.Tokens{
		Token:                 AccessToken,
		TokenExpireIn:         lkdr.DateTimeTZ(now.Add(time.Hour)),
		RefreshToken:          RefreshToken,
		RefreshTokenExpiresIn: pointer.To(lkdr.DateTimeTZ(now.Add(30 * 24 * time.Hour))),
	}
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone        string `json:"phone"`
		CaptchaToken string `json:"captchaToken"`
	}

	_ = json.Unmarshal(s.record(r), &in)
	if in.Phone == "" || in.CaptchaToken == "" {
		writeError(w, http.StatusBadRequest, "validation.error", "phone and captchaToken are required")
		return
	}

	writeJSON(w, http.StatusOK, struct {
		ChallengeToken string                   `json:"challengeToken"`
		ExpiresIn      lkdr.DateTimeMilliOffset `json:"challengeTokenExpiresIn"`
		ExpiresInSec   int                      `json:"challengeTokenExpiresInSec"`
	}{
		ChallengeToken: ChallengeToken,
		ExpiresIn:      lkdr.DateTimeMilliOffset(s.now().Add(5 * time.Minute)),
		ExpiresInSec:   300,
	})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Phone          string `json:"phone"`
		ChallengeToken string `json:"challengeToken"`
		Code           string `json:"code"`
	}

	_ = json.Unmarshal(s.record(r), &in)
	if in.Phone == "" || in.ChallengeToken == "" || in.Code == "" {
		writeError(w, http.StatusBadRequest, "validation.error", "phone, challengeToken and code are required")
		return
	}

	writeJSON(w, http.StatusOK, s.tokens())
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshToken string `json:"refreshToken"`
	}

	_ = json.Unmarshal(s.record(r), &in)
	if in.RefreshToken != RefreshToken {
		writeError(w, http.StatusUnauthorized, "auth.token.invalid", "unknown refresh token")
		return
	}

	writeJSON(w, http.StatusOK, s.tokens())
}

func (s *Server) handleReceipt(w http.ResponseWriter, r *http.Request) {
	var in lkdr.ReceiptIn
	if err := json.Unmarshal(s.record(r), &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation.error", "invalid request body")
		return
	}

	filtered := make([]lkdr.Receipt, 0, len(receipts))
	for _, receipt := range receipts {
		if in.DateFrom != nil && receipt.ReceiveDate.Time().Before(in.DateFrom.Time()) {
			continue
		}

		if in.DateTo != nil && receipt.ReceiveDate.Time().After(in.DateTo.Time()) {
			continue
		}

		filtered = append(filtered, receipt)
	}

	end := in.Offset + in.Limit
	if end > len(filtered) || in.Limit <= 0 {
		end = len(filtered)
	}

	page := filtered[min(in.Offset, len(filtered)):end]
	writeJSON(w, http.StatusOK, lkdr.ReceiptOut{
		Brands:   brands,
		Receipts: page,
		HasMore:  end < len(filtered),
	})
}

func (s *Server) handleFiscalData(w http.ResponseWriter, r *http.Request) {
	var in lkdr.FiscalDataIn
	if err := json.Unmarshal(s.record(r), &in); err != nil {
		writeError(w, http.StatusBadRequest, "validation.error", "invalid request body")
		return
	}

	data, ok := fiscalData[in.Key]
	if !ok {
		writeError(w, http.StatusNotFound, lkdr.ReceiptFiscalDataNotFound, "fiscal data not found for key "+in.Key)
		return
	}

	writeJSON(w, http.StatusOK, data)
}
