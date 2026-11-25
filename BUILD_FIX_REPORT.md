=========== Docker build timeout diagnosis ===========

1) checking .dockerignore
   .dockerignore exists

2) inspecting Dockerfile
   Dockerfile uses selective COPY (good)
   Multi-stage build detected (good)

3) attempting build WITHOUT --no-cache (faster, cache is valid)
   This uses cached layers where possible. If dependencies haven't changed,
   this should complete in <5 minutes instead of 60+.
   Running: docker compose build app
   ✓ Build succeeded in 4.4s

4) recreating app container
   ✓ Container recreated

5) waiting for app to become healthy
   ✓ App is healthy after 6s

6) running vet suite
preflight: https://10.152.152.11:8443 /login -> 200

=== buyer ===
buyer: 3 failed checks, 3 findings -> vet/vet_buyer.md

=== vendor ===
vendor: 4 failed checks, 1 findings -> vet/vet_vendor.md

=== operator ===
operator: 1 failed checks, 2 findings -> vet/vet_operator.md

=== admin ===
admin: 2 failed checks, 3 findings -> vet/vet_admin.md
Consolidated 8 reports -> /home/oniondev/OnionShop/vet/CONSOLIDATED_REPORT.md
Roles: admin, buyer, operator (mod), operator, vendor
Checks: 75  Failed: 16  Findings: 9
Shared failures: 1  Role-specific: 10

Combined: vet/VET_REPORT.md


⚠ Vet suite exited with code 1
Check vet/VET_REPORT.md for details

7) consolidating reports
Consolidated 8 reports -> /home/oniondev/OnionShop/vet/CONSOLIDATED_REPORT.md
Roles: admin, buyer, operator (mod), operator, vendor
Checks: 75  Failed: 16  Findings: 9
Shared failures: 1  Role-specific: 10
