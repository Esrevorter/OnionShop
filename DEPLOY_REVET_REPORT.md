=========== evidence -> repair -> redeploy -> revet ===========

1) live fixture + schema evidence
   vendor row: devendortest|t|0
   fee ledger: 0
   failing order ownership:
     e84892b6-5285-49c1-954b-d3d95e788339 -> in_escrow|dekoperman
     cb652247-7a6a-4edc-98a3-6e5ee4163667 -> in_escrow|dekoperman
   buyer identity in roles.json: dekoperman
   migrations: disk=22 applied_max=10
   containers:
     app Up 4 minutes (healthy)
     backup Up 2 hours
     monero-wallet-rpc Up 2 hours
     monerod Up 2 hours (unhealthy)
     nginx Up 2 hours (healthy)
     postgres Up 2 hours (healthy)
     redis Up 2 hours (healthy)

2) vet_admin no longer leaves a positive fee behind
   admin vet restores fee=0 after testing

3) fixture repair (fee gate + PGP)
   admin login: ok
   fee set to 0: 200
   vendor login: ok
   PGP re-upload: 200
   /vendor/shipping now: 200

4) rebuilding app image from current HEAD (this was the missing step)
   build rc=0
   recreate rc=0
   /health: 200

5) re-running consolidated vet