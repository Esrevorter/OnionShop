=========== UI polish v2 ===========

1) terminology
   help links after fix:
     layout.html  /help/admins  'Admin Guide'
     page_help.html  /help/admins  'Operating Approvals, fees, disputes, withdrawals, automated timers, and OpSec rules. Admin guide'

2) CSS polish layer
   wrote web/static/polish.css

3) error handling markup
   AuthData gains Errors map[string]string

4) crypto copy affordance

5) gates
   OK   go build ./...
   OK   go vet ./internal/server
   OK   go test ./internal/server/... -count=1

6) changes applied
   + terminology: web/templates/layout.html
   + terminology: web/templates/page_admin_orders.html
   + terminology: web/templates/page_help_admins.html
   + terminology: web/templates/page_order_detail.html
   + terminology: web/templates/page_orders.html
   + terminology: web/templates/page_vendor_stats.html
   + terminology: web/templates/page_wallet.html
   + layout links polish.css
   + AuthData gains Errors map
   + field-error wired: page_checkout.html refund_address
   + field-error wired: page_vendor_product_edit.html price_usd
   + field-error wired: page_vendor_product_edit.html title
   + field-error wired: page_vendor_product_edit.html stock
   + crypto wrapping: web/templates/page_account.html
   + crypto wrapping: web/templates/page_admin_withdrawals.html

   flagged:
     - relabelled 'Operator Guide' -> 'Admin Guide' in web/templates/layout.html
   VERIFIED origin/main == HEAD

Follow-up: field-level errors need handler wiring at these sites:
   auth_handlers.go: challenge failed, please try again
   auth_handlers.go: weak password: must be 10-128 characters
   auth_handlers.go: passwords do not match
   auth_handlers.go: that username is unavailable
   auth_handlers.go: registration failed, please try again
   auth_handlers.go: login failed, please try again
   auth_handlers.go: this account cannot sign in right now
   auth_handlers.go: submission rejected
   auth_handlers.go: could not save the phrase, please try again
   auth_handlers.go: invalid authentication code
   auth_handlers.go: account error
   auth_handlers.go: invalid current password
   auth_handlers.go: setup session expired, please restart
   auth_handlers.go: could not enable 2FA
   auth_handlers.go: could not disable 2FA
   checkout_handlers.go: Human verification failed, please try again.
   checkout_handlers.go: Please provide a delivery address / instructions.
   checkout_handlers.go: Delivery instructions are too long (max 2000 characters).
   checkout_handlers.go: Please provide a refund XMR address (used if the order is refunded).
   checkout_handlers.go: Internal server error: database pool unavailable.
   checkout_handlers.go: Invalid user session.
   checkout_handlers.go: An item in your cart is no longer available. Please refresh your cart.
   checkout_handlers.go: Failed to load shipping options.
   checkout_handlers.go: Please choose a shipping method for every vendor.
   checkout_handlers.go: Invalid shipping selection.
   checkout_handlers.go: Failed to create orders. Please try again.
   checkout_handlers.go: An item in your cart went out of stock. Please refresh your cart.
   checkout_handlers.go: Failed to finalize order totals.
   checkout_handlers.go: Failed to generate payment address.
   checkout_handlers.go: Failed to save payment details.
   vendor_handlers.go: this account cannot request vendor status
   vendor_handlers.go: challenge failed, please try again
   vendor_handlers.go: vendor application failed, please try again
   withdrawal_handlers.go: Human verification failed, please try again.
   withdrawal_handlers.go: service unavailable
   withdrawal_handlers.go: only approved vendors can request withdrawals
   withdrawal_handlers.go: monero address is required
   withdrawal_handlers.go: withdrawal service unavailable
   pgp.go: submission rejected
   pgp.go: challenge failed, please try again
   pgp.go: could not save the key, please try again
   pgp.go: could not remove the key, please try again
   admin_handlers.go: Category name must be 2-50 characters.
   admin_handlers.go: Category slug must be 2-50 characters.
   admin_handlers.go: Slug can only contain lowercase letters, numbers, and hyphens.
   admin_handlers.go: Could not create category. Slug may already exist.
   product_handlers.go: challenge failed, please try again
   product_handlers.go: no approved vendor profile on this account
   product_handlers.go: Internal server error.
   product_handlers.go: invalid form data or upload too large (max 10MB)
   product_handlers.go: listing failed, please try again