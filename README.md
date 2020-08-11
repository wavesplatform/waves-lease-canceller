# waves-lease-canceller
Simple utility to cancel all active leases on Waves account.

## Usage

Test parameters with DRY-RUN flag
```bash
./waves-lease-canceller -node-api https://nodes.wavesnodes.com -account-sk [Base58 encoded account seed] -dry-run
```

Real-life execution
```bash
./waves-lease-canceller -node-api https://nodes.wavesnodes.com -account-sk [Base58 encoded account seed]
```