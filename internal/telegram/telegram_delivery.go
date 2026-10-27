package telegram

import (
	"context"
	"math"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type telegramDeliveryError string

func (e telegramDeliveryError) Error() string { return string(e) }

func telegramDelivery(kind tg.AuthSentCodeTypeClass) (string, string, int, bool) {
	switch v := kind.(type) {
	case *tg.AuthSentCodeTypeApp:
		return "app", "Telegram 返回的投递方式为客户端消息，请查看已登录设备中的 Telegram 服务消息（不是短信）；这不代表已确认送达", v.GetLength(), true
	case *tg.AuthSentCodeTypeSMS:
		return "sms", "Telegram 返回的投递方式为短信，请查看手机短信；这不代表已确认送达", v.GetLength(), true
	case *tg.AuthSentCodeTypeCall:
		return "call", "Telegram 将通过语音电话告知数字验证码", v.GetLength(), true
	case *tg.AuthSentCodeTypeEmailCode:
		return "email", "暂不支持邮箱验证码，请使用官方 Telegram 客户端完成验证", 0, false
	case *tg.AuthSentCodeTypeSetUpEmailRequired:
		return "setup_email", "暂不支持设置登录邮箱，请使用官方 Telegram 客户端完成设置", 0, false
	case *tg.AuthSentCodeTypeFirebaseSMS:
		return "firebase_sms", "暂不支持 Firebase 短信验证，请使用官方 Telegram 客户端", 0, false
	case *tg.AuthSentCodeTypeFragmentSMS:
		return "fragment_sms", "暂不支持 Fragment 验证，请使用官方 Telegram 客户端", 0, false
	case *tg.AuthSentCodeTypeFlashCall:
		return "flash_call", "暂不支持闪呼验证，请使用官方 Telegram 客户端", 0, false
	case *tg.AuthSentCodeTypeMissedCall:
		return "missed_call", "暂不支持未接来电验证，请使用官方 Telegram 客户端", 0, false
	case *tg.AuthSentCodeTypeSMSWord:
		return "sms_word", "暂不支持短信单词验证码，请使用官方 Telegram 客户端", 0, false
	case *tg.AuthSentCodeTypeSMSPhrase:
		return "sms_phrase", "暂不支持短信短语验证码，请使用官方 Telegram 客户端", 0, false
	default:
		return "unknown", "暂不支持此 Telegram 验证方式，请使用官方 Telegram 客户端", 0, false
	}
}

func telegramNextDelivery(kind tg.AuthCodeTypeClass) string {
	switch kind.(type) {
	case *tg.AuthCodeTypeSMS:
		return "sms"
	case *tg.AuthCodeTypeCall:
		return "call"
	default:
		return ""
	}
}

func (s *telegramService) expirePending(now time.Time) {
	if !s.expires.IsZero() && !now.Before(s.expires) {
		s.clearPending()
		s.state = "idle"
	}
}

func (s *telegramService) acceptDelivery(phone string, result tg.AuthSentCodeClass, now time.Time) {
	s.clearPending()
	if _, ok := result.(*tg.AuthSentCodeSuccess); ok {
		s.state = "authorized"
		return
	}
	s.state = "unsupported"
	s.delivery, s.deliveryMessage, s.codeLength, _ = telegramDelivery(nil)
	s.deliveryError = s.deliveryMessage
	code, ok := result.(*tg.AuthSentCode)
	if !ok {
		return
	}
	var supported bool
	s.delivery, s.deliveryMessage, s.codeLength, supported = telegramDelivery(code.GetType())
	s.deliveryError = s.deliveryMessage
	if supported {
		s.state = "code"
		s.deliveryError = ""
	}
	s.phone, s.hash = phone, code.GetPhoneCodeHash()
	if next, present := code.GetNextType(); present {
		s.nextType = next
	}
	// An absent timeout is not permission to immediately resend.
	if seconds, present := code.GetTimeout(); present && seconds >= 0 {
		s.resendAt = now.Add(time.Duration(seconds) * time.Second)
	}
	s.expires = now.Add(10 * time.Minute)
	s.expiry = time.AfterFunc(time.Until(s.expires), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.expirePending(time.Now())
	})
}

func (s *telegramService) resendReady(now time.Time) bool {
	return (s.state == "code" || s.state == "unsupported") && s.phone != "" && s.hash != "" &&
		now.Before(s.expires) && telegramNextDelivery(s.nextType) != "" && !s.resendAt.IsZero() &&
		!now.Before(s.resendAt) && !now.Before(s.floodUntil)
}

func (s *telegramService) deliverySnapshot(st *TelegramState, now time.Time) {
	if st.Authorized {
		return
	}
	st.Delivery, st.DeliveryMessage, st.CodeLength = s.delivery, s.deliveryMessage, s.codeLength
	st.NextDelivery = telegramNextDelivery(s.nextType)
	st.Error = s.deliveryError
	deadline := s.resendAt
	if s.floodUntil.After(deadline) {
		deadline = s.floodUntil
	}
	if now.Before(deadline) {
		st.ResendAfter = int(math.Ceil(deadline.Sub(now).Seconds()))
	}
	st.CanResend = s.resendReady(now)
}

func (s *telegramService) deliveryFailure(err error) {
	if delay, ok := tgerr.AsFloodWait(err); ok {
		until := time.Now().Add(delay)
		if until.After(s.floodUntil) {
			s.floodUntil = until
		}
	}
	if tgerr.Is(err, "PHONE_CODE_EXPIRED") {
		s.clearPending()
		s.state = "idle"
	}
}

func (s *telegramService) ResendCode(ctx context.Context) (TelegramState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resendCode(ctx, s.operation, func(ctx context.Context, request *tg.AuthResendCodeRequest) (tg.AuthSentCodeClass, error) {
		return s.client.API().AuthResendCode(ctx, request)
	})
}

func (s *telegramService) resendCode(ctx context.Context,
	prepare func(context.Context) (context.Context, context.CancelFunc, error),
	request func(context.Context, *tg.AuthResendCodeRequest) (tg.AuthSentCodeClass, error),
) (TelegramState, error) {
	s.expirePending(time.Now())
	if !s.resendReady(time.Now()) {
		return s.snapshot(telegramDeliveryError("当前无法重发：请等待冷却结束，或在登录请求过期后重新开始；仅支持服务器允许的短信或语音重发"))
	}
	phone, hash := s.phone, s.hash
	ctx, cancel, err := prepare(ctx)
	defer cancel()
	if err != nil {
		return s.snapshot(err)
	}
	if !s.resendReady(time.Now()) || phone != s.phone || hash != s.hash {
		return s.snapshot(telegramDeliveryError("登录请求已失效，请重新开始"))
	}
	result, err := request(ctx, &tg.AuthResendCodeRequest{PhoneNumber: phone, PhoneCodeHash: hash})
	if err != nil {
		s.deliveryFailure(err)
		return s.snapshot(err)
	}
	s.acceptDelivery(phone, result, time.Now())
	return s.snapshot(nil)
}
