package server

import "os"

// captchaTextAlt keeps the challenge machine-readable only when enabled (dev/test
// automation + screen-reader testing). Production sets CAPTCHA_TEXT_ALT=0 so the
// prompt exists only inside the image; rate limits + honeypot remain the bot boundary.
func captchaTextAlt() bool { return os.Getenv("CAPTCHA_TEXT_ALT") != "0" }
