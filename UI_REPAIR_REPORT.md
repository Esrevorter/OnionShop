=========== polish repair ===========

1) unwrapping <code class="crypto"> inside textareas
   cleaned web/templates/page_account.html

2) removing main{} layout override
   removed main{} from web/static/polish.css

3) live /account replace-key form (evidence for the button blob)
   login: ok
<form method="post" action="/account/pgp" autocomplete="off">
<input type="hidden" name="csrf_token" value="7E14Q_0HLVjquMaZL9IKnfTYsRMf3RnsXfzswEOAQf4">
<div class="captcha-block" aria-label="Human verification">
  <p><strong>Human verification</strong> <span class="form-help">Please solve this short challenge.</span></p>
  <input type="hidden" name="captcha_id" value="ZlH25vrrD9vJpPXMlMjeTQ.twu7WCOD0kOAJu6m">
  <input type="hidden" name="captcha_token" value="Ii7uowhAhL-JGE2Wqhbl_w.UHpHBvorTh8UloRw">
  <p>11 &#43; 13 =</p>
  <img class="captcha-img" src="/captcha/ZlH25vrrD9vJpPXMlMjeTQ.twu7WCOD0kOAJu6m" alt="Visual CAPTCHA challenge">
  <label for="captcha-answer">Answer</label><input id="captcha-answer" type="text" name="captcha_answer" autocomplete="off" maxlength="4" inputmode="numeric" required>
</div>
<div class="hp" aria-hidden="true"><label>Leave this field empty<input type="text" name="sta" tabindex="-1" autocomplete="off" autocorrect="off" autocapitalize="off" spellcheck="false"></label></div>
      <input type="hidden" name="csrf_token" value="7E14Q_0HLVjquMaZL9IKnfTYsRMf3RnsXfzswEOAQf4">
      <label for="pgp_public_key">ASCII-armored PUBLIC key block</label>
      <textarea id="pgp_public_key" name="pgp_public_key" rows="8" spellcheck="false">&lt;code class="crypto">-----BEGIN PGP PUBLIC KEY BLOCK-----
xjMEawadkhYJKwYBBAHaRw8BAQdAG5ipcdizmb6roClpQqS5oLGTTetejm/vOnuj
XFhWWi7NG1ZldCBGaXh0dXJlIDx2ZXRAbG9jYWxob3N0PsKQBBMWCgA4FiEE0kM2
QjFw4lPy0TPwqSwsMrOEkjYFAmsGnZIC

4) gates + deploy
   OK   go build ./...
   OK   go test ./internal/server/... -count=1
   VERIFIED origin/main == HEAD