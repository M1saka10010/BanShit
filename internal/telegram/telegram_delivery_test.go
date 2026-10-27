package telegram

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func deliveryCode(kind tg.AuthSentCodeTypeClass, timeout int) *tg.AuthSentCode {
	c := &tg.AuthSentCode{Type: kind, PhoneCodeHash: "private-hash"}
	c.SetNextType(&tg.AuthCodeTypeSMS{})
	c.SetTimeout(timeout)
	return c
}

func TestTelegramDeliveryTypes(t *testing.T) {
	for _, tc := range []struct {
		kind      tg.AuthSentCodeTypeClass
		name      string
		supported bool
	}{
		{&tg.AuthSentCodeTypeApp{Length: 5}, "app", true},
		{&tg.AuthSentCodeTypeSMS{Length: 5}, "sms", true},
		{&tg.AuthSentCodeTypeCall{Length: 5}, "call", true},
		{&tg.AuthSentCodeTypeEmailCode{EmailPattern: "private-email"}, "email", false},
		{&tg.AuthSentCodeTypeSetUpEmailRequired{}, "setup_email", false},
		{&tg.AuthSentCodeTypeFirebaseSMS{}, "firebase_sms", false},
		{&tg.AuthSentCodeTypeFragmentSMS{}, "fragment_sms", false},
		{&tg.AuthSentCodeTypeFlashCall{}, "flash_call", false},
		{&tg.AuthSentCodeTypeMissedCall{}, "missed_call", false},
		{&tg.AuthSentCodeTypeSMSWord{}, "sms_word", false},
		{&tg.AuthSentCodeTypeSMSPhrase{}, "sms_phrase", false},
		{nil, "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &telegramService{}
			defer s.clearPending()
			s.acceptDelivery("private-phone", deliveryCode(tc.kind, 20), time.Now())
			st, err := s.snapshot(nil)
			if err != nil || st.Delivery != tc.name || st.NextDelivery != "sms" || st.CanResend || st.ResendAfter != 20 {
				t.Fatalf("%+v %v", st, err)
			}
			if tc.supported {
				if st.State != "code" || st.CodeLength != 5 || st.Error != "" {
					t.Fatal(st)
				}
			} else if st.State != "unsupported" || !strings.Contains(st.Error, "暂不支持") || strings.Contains(st.DeliveryMessage, "已发送") {
				t.Fatal(st)
			}
			b, _ := json.Marshal(st)
			if strings.Contains(string(b), "private") {
				t.Fatal("private data leaked")
			}
			s.resendAt = time.Now().Add(-time.Second)
			if !s.resendReady(time.Now()) {
				t.Fatal("fallback challenge lost")
			}
		})
	}
}

func TestTelegramDeliveryTimeoutFlags(t *testing.T) {
	s := &telegramService{}
	defer s.clearPending()
	c := &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{}, PhoneCodeHash: "hash", NextType: &tg.AuthCodeTypeSMS{}, Timeout: 0}
	s.acceptDelivery("phone", c, time.Now())
	if s.nextType != nil || !s.resendAt.IsZero() || s.resendReady(time.Now()) {
		t.Fatal("ignored optional flags")
	}
	c.SetNextType(&tg.AuthCodeTypeCall{})
	s.acceptDelivery("phone", c, time.Now())
	if s.resendReady(time.Now()) {
		t.Fatal("missing timeout permits resend")
	}
	c.SetTimeout(-1)
	s.acceptDelivery("phone", c, time.Now())
	if s.resendReady(time.Now()) {
		t.Fatal("negative timeout permits resend")
	}
	c.SetTimeout(0)
	s.acceptDelivery("phone", c, time.Now())
	if !s.resendReady(time.Now()) {
		t.Fatal("explicit zero timeout blocked")
	}
}

func deliveryPrepare(ctx context.Context) (context.Context, context.CancelFunc, error) {
	return ctx, func() {}, nil
}

func TestTelegramResendGuards(t *testing.T) {
	for _, name := range []string{"expired", "cooldown", "no-next", "unsupported-next", "no-phone", "no-hash", "reconnect", "expires-during-connect"} {
		t.Run(name, func(t *testing.T) {
			s := &telegramService{}
			defer s.clearPending()
			s.acceptDelivery("phone", deliveryCode(&tg.AuthSentCodeTypeApp{}, 0), time.Now())
			prepare := deliveryPrepare
			switch name {
			case "expired":
				s.expires = time.Now().Add(-time.Second)
			case "cooldown":
				s.resendAt = time.Now().Add(time.Minute)
			case "no-next":
				s.nextType = nil
			case "unsupported-next":
				s.nextType = &tg.AuthCodeTypeFlashCall{}
			case "no-phone":
				s.phone = ""
			case "no-hash":
				s.hash = ""
			case "reconnect":
				prepare = func(ctx context.Context) (context.Context, context.CancelFunc, error) {
					s.clearPending()
					s.state = "idle"
					return deliveryPrepare(ctx)
				}
			case "expires-during-connect":
				prepare = func(ctx context.Context) (context.Context, context.CancelFunc, error) {
					s.expires = time.Now().Add(-time.Second)
					return deliveryPrepare(ctx)
				}
			}
			_, err := s.resendCode(context.Background(), prepare, func(context.Context, *tg.AuthResendCodeRequest) (tg.AuthSentCodeClass, error) {
				t.Fatal("unexpected RPC")
				return nil, nil
			})
			if err == nil {
				t.Fatal("guard accepted")
			}
		})
	}
}

func TestTelegramResendUpdatesAndAuthorized(t *testing.T) {
	s := &telegramService{}
	defer s.clearPending()
	s.acceptDelivery("phone", deliveryCode(&tg.AuthSentCodeTypeEmailCode{}, 0), time.Now())
	st, err := s.resendCode(context.Background(), deliveryPrepare, func(_ context.Context, req *tg.AuthResendCodeRequest) (tg.AuthSentCodeClass, error) {
		if req.PhoneNumber != "phone" || req.PhoneCodeHash != "private-hash" {
			t.Fatal("wrong challenge")
		}
		c := deliveryCode(&tg.AuthSentCodeTypeSMS{Length: 6}, 30)
		c.PhoneCodeHash = "new-hash"
		c.SetNextType(&tg.AuthCodeTypeCall{})
		return c, nil
	})
	if err != nil || st.State != "code" || st.Delivery != "sms" || st.NextDelivery != "call" || st.CodeLength != 6 || st.ResendAfter != 30 || s.hash != "new-hash" {
		t.Fatalf("%+v %v", st, err)
	}
	s.acceptDelivery("phone", &tg.AuthSentCodeSuccess{}, time.Now())
	st, err = s.SendCode(context.Background(), "+1234567")
	if err != nil || !st.Authorized || st.Delivery != "" || st.DeliveryMessage != "" || st.CanResend || s.hash != "" {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestTelegramDeliveryFloodWaitAndTimeout(t *testing.T) {
	s := &telegramService{}
	defer s.clearPending()
	s.acceptDelivery("phone", deliveryCode(&tg.AuthSentCodeTypeApp{}, 0), time.Now())
	calls := 0
	rpc := func(context.Context, *tg.AuthResendCodeRequest) (tg.AuthSentCodeClass, error) {
		calls++
		return nil, &tgerr.Error{Code: 420, Type: "FLOOD_WAIT", Argument: 60}
	}
	st, err := s.resendCode(context.Background(), deliveryPrepare, rpc)
	if err == nil || st.CanResend || st.ResendAfter != 60 {
		t.Fatalf("%+v %v", st, err)
	}
	s.resendCode(context.Background(), deliveryPrepare, rpc)
	if calls != 1 {
		t.Fatal("repeated flood RPC")
	}
	s.clearPending()
	s.state = "idle"
	st, err = s.SendCode(context.Background(), "+1234567")
	if err == nil || st.ResendAfter != 60 {
		t.Fatal("send bypassed flood")
	}
	s.floodUntil = time.Time{}
	s.acceptDelivery("phone", deliveryCode(&tg.AuthSentCodeTypeApp{}, 0), time.Now())
	calls = 0
	_, err = s.resendCode(context.Background(), deliveryPrepare, func(context.Context, *tg.AuthResendCodeRequest) (tg.AuthSentCodeClass, error) {
		calls++
		return nil, context.DeadlineExceeded
	})
	if err == nil || calls != 1 || s.hash != "private-hash" {
		t.Fatal("timeout retried or lost challenge")
	}
	if _, err = s.SendCode(context.Background(), "+7654321"); err == nil {
		t.Fatal("pending send bypass")
	}
}
