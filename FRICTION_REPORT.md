=========== friction policy ===========

1) every CAPTCHA gate and its purpose expression
   adminDisputeResolve                purpose="admin"   (admin_dispute_handlers.go)
   adminTicketStaffEvent              purpose="admin"   (admin_ticket_handlers.go)
   handle2FADisable                   purpose="account"   (auth_handlers.go)
   handle2FAEnable                    purpose="account"   (auth_handlers.go)
   handle2FALoginSubmit               purpose="login2fa"   (auth_handlers.go)
   handleAdminApproveVendor           purpose="admin"   (admin_vendor_queue.go)
   handleAdminBanUser                 purpose="admin"   (admin_handlers.go)
   handleAdminCategoryCreate          purpose="admin"   (admin_handlers.go)
   handleAdminCreateUser              purpose="admin"   (admin_user_admin.go)
   handleAdminForceCancel             purpose="admin"   (admin_order_handlers.go)
   handleAdminKillSessions            purpose="admin"   (admin_session_handlers.go)
   handleAdminRejectVendor            purpose="admin"   (admin_vendor_queue.go)
   handleAdminResetPassword           purpose="admin"   (admin_handlers.go)
   handleAdminSetRole                 purpose="admin"   (admin_user_admin.go)
   handleAdminTicketReply             purpose="admin"   (admin_ticket_handlers.go)
   handleCheckoutSubmit               purpose="checkout"   (checkout_handlers.go)
   handleLoginSubmit                  purpose="login"   (auth_handlers.go)
   handleOrderCancel                  purpose="order"   (order_actions.go)
   handleOrderExtend                  purpose="order"   (order_actions.go)
   handleOrderRelease                 purpose="order"   (order_actions.go)
   handleOrderShip                    purpose="order"   (order_actions.go)
   handlePGPClear                     purpose=accountPurpose   (pgp.go)
   handlePGPUpload                    purpose=accountPurpose   (pgp.go)
   handlePhraseSubmit                 purpose="account"   (auth_handlers.go)
   handleProductCreate                purpose=productPurpose   (product_handlers.go)
   handleProductUpdate                purpose=productPurpose   (product_handlers.go)
   handleRegisterSubmit               purpose="register"   (auth_handlers.go)
   handleReviewSubmit                 purpose=reviewPurpose   (vendor_handlers.go)
   handleShipsFromSet                 purpose="account"   (ships_from.go)
   handleTicketCreate                 purpose="ticket"   (ticket_handlers.go)
   handleTicketReply                  purpose="ticket"   (ticket_handlers.go)
   handleVendorApplySubmit            purpose=vendorPurpose   (vendor_handlers.go)
   handleVendorDecision               purpose=vendorPurpose   (vendor_handlers.go)
   handleVendorMessage                purpose=messagePurpose   (vendor_handlers.go)
   handleWithdrawSubmit               purpose="withdraw"   (withdrawal_handlers.go)
   registerPageRoutes                 purpose=purpose   (pages.go)

2) privileged POST routes -> remove CAPTCHA
   privileged handlers: admin, adminOnly, handleAdminApproveVendor, handleAdminAudit, handleAdminBanUser, handleAdminCategories, handleAdminCategoryCreate, handleAdminCreateUser, handleAdminDashboard, handleAdminDisputeDetail, handleAdminDisputeRefund, handleAdminDisputeRelease, handleAdminDisputes, handleAdminForceCancel, handleAdminKillSessions, handleAdminOrders, handleAdminRejectVendor, handleAdminResetPassword, handleAdminReviewWithdrawal, handleAdminSetRole, handleAdminSettings, handleAdminSettingsPost, handleAdminTicketClose, handleAdminTicketDetail, handleAdminTicketReply, handleAdminTicketResolve, handleAdminTickets, handleAdminUserSessions, handleAdminUsers, handleAdminVendors, handleAdminWithdrawals, handleOrderShip, handleProductCreate, handleProductPublish, handleProductRemove, handleProductUpdate, handleResolveDispute, handleShippingCreate, handleShippingDelete, handleShippingToggle, handleVendorDecision, handleVendorFeePost, modOnly, staff, vendor, vendorOrAdmin
   - CAPTCHA removed from handleAdminApproveVendor
   - CAPTCHA removed from handleAdminBanUser
   - CAPTCHA removed from handleAdminCategoryCreate
   - CAPTCHA removed from handleAdminCreateUser
   - CAPTCHA removed from handleAdminForceCancel
   - CAPTCHA removed from handleAdminKillSessions
   - CAPTCHA removed from handleAdminRejectVendor
   - CAPTCHA removed from handleAdminResetPassword
   - CAPTCHA removed from handleAdminSetRole
   - CAPTCHA removed from handleAdminTicketReply
   - CAPTCHA removed from handleOrderShip
   - CAPTCHA removed from handleProductCreate
   - CAPTCHA removed from handleProductUpdate
   - CAPTCHA removed from handleVendorDecision

3) session trust grant (verify once, bounded window)
   + grant key, TTL, sensitivity rule
   requireFriction honours the grant; sensitive purposes always challenge

4) permanent guard against this bug class
   + friction_policy_test.go

5) gates
# github.com/example/onionshop/internal/server
internal/server/admin_handlers.go:114:2: declared and not used: fail

   FAIL go build ./...
# github.com/example/onionshop/internal/server
internal/server/admin_handlers.go:114:2: declared and not used: fail
