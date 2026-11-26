=========== PGP poison cleanup ===========

1) cleaning stored PGP keys
   poisoned rows: 0
   poisoned rows after clean: 0
   first line of devendortest key: -----BEGIN PGP PUBLIC KEY BLOCK-----

2) hardening PGP upload handler
   already hardened or anchor not found

3) honeypot visibility guard
   guard added to web/static/polish.css

4) gates + deploy
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1

5) verify rendered /account key
   textarea starts with: 
   honeypot visible in CSS: guard present
   button markup: vxoYzdK9jqrH2xUFttSxeZ2JYb3bStQBALaDfYqNpkqs kFTZLcet5ow9neuqJZCP/DG&#43;P8/Y9ZYD =Gu&#43;o -----END PGP PUBLIC KEY BLOCK-----</textarea> <div class="form-actions"><button type="submit"><code class="crypto"></code>Replace Key</button></div> </form> </div> </section> <div class="auth-card"> 
   VERIFIED origin/main == HEAD