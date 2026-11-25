=========== redeploy + deterministic revet ===========

1) rebuilding app image (source was fixed but image was stale)
   build rc=0
   recreate rc=0
   /health rc=0

2) admin probes

3) vendor publishes a known listing

4) buyer purchases the fixture listing

5) results
   admin: 2 checks, 0 failed -> vet/revet_admin.md
   vendor: 6 checks, 3 failed -> vet/revet_vendor.md
   buyer: 2 checks, 1 failed -> vet/revet_buyer.md