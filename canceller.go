package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/oguzbilgic/fpd"
	"github.com/wavesplatform/gowaves/pkg/client"
	"github.com/wavesplatform/gowaves/pkg/crypto"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

const (
	defaultScheme        = "http"
	standardFee   uint64 = 100000
)

var (
	version              = "v0.0.0"
	interruptSignals     = []os.Signal{os.Interrupt}
	errInvalidParameters = errors.New("invalid parameters")
	errUserTermination   = errors.New("user termination")
	errFailure           = errors.New("operation failure")
)

type AddressesExtraFee struct {
	ExtraFee uint64 `json:"extraFee"`
}

func main() {
	err := run()
	if err != nil {
		switch err {
		case errInvalidParameters:
			showUsage()
			os.Exit(2)
		case errUserTermination:
			os.Exit(130)
		case errFailure:
			os.Exit(70)
		default:
			os.Exit(1)
		}
	}
}

func run() error {
	var (
		nodeURL     string
		accountSK   string
		accountPK   string
		dryRun      bool
		showHelp    bool
		showVersion bool
	)
	flag.StringVar(&nodeURL, "node-api", "http://localhost:6869", "Node's REST API URL")
	flag.StringVar(&accountSK, "account-sk", "", "Base58 encoded private key of the account")
	flag.StringVar(&accountPK, "account-pk", "", "Base58 encoded public key of the account")
	flag.BoolVar(&dryRun, "dry-run", false, "Test execution without creating real transactions on blockchain")
	flag.BoolVar(&showHelp, "help", false, "Show usage information and exit")
	flag.BoolVar(&showVersion, "version", false, "Print version information and quit")
	flag.Parse()

	if showHelp {
		showUsage()
		return nil
	}
	if showVersion {
		fmt.Printf("Waves Leasing Canceller %s\n", version)
		return nil
	}
	if nodeURL == "" || len(strings.Fields(nodeURL)) > 1 {
		log.Printf("[ERROR] Invalid node's URL '%s'", nodeURL)
		return errInvalidParameters
	}
	if accountSK == "" || len(strings.Fields(accountSK)) > 1 {
		log.Printf("[ERROR] Invalid generating account private key '%s'", accountSK)
		return errInvalidParameters
	}
	if accountPK == "" || len(strings.Fields(accountPK)) > 1 {
		log.Print("[INFO] No different account public key is given")
	}
	if dryRun {
		log.Print("[INFO] DRY-RUN: No actual transactions will be created")
	}

	ctx := interruptListener()

	// 1. Check connection to node's API
	cl, err := nodeClient(ctx, nodeURL)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return errUserTermination
		}
		log.Printf("[ERROR] Failed to connect to node at '%s': %v", nodeURL, err)
		return errFailure
	}
	log.Printf("[INFO] Successfully connected to '%s'", cl.GetOptions().BaseUrl)

	// 2. Acquire the network scheme from genesis block
	scheme, err := getScheme(ctx, cl)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return errUserTermination
		}
		log.Printf("[ERROR] Failed to aquire blockchain scheme: %v", err)
		return errFailure
	}
	log.Printf("[INFO] Blockchain scheme: %s", string(scheme))

	// 3. Generate public keys and addresses from given private key
	sk, pk, addr, err := parseSK(scheme, accountSK)
	if err != nil {
		log.Printf("[ERROR] Failed to parse account's private key: %v", err)
		return errFailure
	}
	if accountPK != "" {
		pk, err = crypto.NewPublicKeyFromBase58(accountPK)
		if err != nil {
			log.Printf("[ERROR] Failed to parse additional public key: %v", err)
			return errFailure
		}
		addr, err = proto.NewAddressFromPublicKey(scheme, pk)
		if err != nil {
			log.Printf("[ERROR] Failed to parse account's address: %v", err)
			return errFailure
		}
	}
	log.Printf("[INFO] Account's public key: %s", pk.String())
	log.Printf("[INFO] Account's address: %s", addr.String())

	// 4. Get active leasing transactions
	leasings, total, err := getActiveLeasings(ctx, cl, addr)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return errUserTermination
		}
		log.Printf("[ERROR] Failed to get active leasings: %v", err)
		return errFailure
	}
	log.Printf("[INFO] Found %d active leasings on account '%s' with the total amount of %s", len(leasings), addr.String(), format(total))

	// 4. Create cancel leasing transactions
	leaseExtraFee, err := getExtraFee(ctx, cl, addr)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return errUserTermination
		}
		log.Printf("[ERROR] Failed to check extra fee on account '%s': %v", addr.String(), err)
		return errFailure
	}
	if leaseExtraFee != 0 {
		log.Printf("[INFO] Extra fee on cancel leasing: %s", format(leaseExtraFee))
	} else {
		log.Print("[INFO] No extra fee on cancel leasing")
	}
	fee := standardFee + leaseExtraFee
	for i, lease := range leasings {
		cancel := proto.NewUnsignedLeaseCancelWithProofs(2, scheme, pk, lease, fee, timestamp())
		err = cancel.Sign(scheme, sk)
		if err != nil {
			log.Printf("[ERROR] Failed to sign lease cancel transaction: %v", err)
			return errFailure
		}
		if dryRun {
			b, err := json.Marshal(cancel)
			if err != nil {
				log.Printf("[ERROR] Failed to make transaction json: %v", err)
				return errFailure
			}
			log.Printf("[INFO] Cancel transaction #%d:\n%s", i+1, string(b))
		} else {
			log.Printf("[INFO] Cancel transaction #%d ID: %s", i+1, cancel.ID.String())
			err = broadcast(ctx, cl, cancel)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return errUserTermination
				}
				log.Printf("[ERROR] Failed to broadcast lease transaction: %v", err)
				return errFailure
			}
			err = track(ctx, cl, *cancel.ID)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return errUserTermination
				}
				log.Printf("[ERROR] Failed to track lease cancel transaction: %v", err)
				return errFailure
			}
		}
	}
	log.Printf("[INFO] %d cancel transactions created", len(leasings))
	log.Print("[INFO] OK")
	return nil
}

func broadcast(ctx context.Context, cl *client.Client, tx proto.Transaction) error {
	_, err := cl.Transactions.Broadcast(ctx, tx)
	return err
}

func track(ctx context.Context, cl *client.Client, id crypto.Digest) error {
	log.Printf("[INFO] Waiting for transaction '%s' on blockchain...", id.String())
	for {
		_, rsp, err := cl.Transactions.Info(ctx, id)
		if errors.Is(err, context.Canceled) {
			return err
		}
		if rsp.StatusCode == http.StatusOK {
			return nil
		}
		time.Sleep(time.Second)
	}
}

func timestamp() uint64 {
	return uint64(time.Now().UnixNano()) / 1000000
}

func format(amount uint64) string {
	da := fpd.New(int64(amount), -8)
	return fmt.Sprintf("%s WAVES", da.FormattedString())
}

func getActiveLeasings(ctx context.Context, cl *client.Client, addr proto.Address) ([]crypto.Digest, uint64, error) {
	txs, _, err := cl.Leasing.Active(ctx, addr)
	if err != nil {
		return nil, 0, err
	}
	var amount uint64 = 0
	r := make([]crypto.Digest, len(txs))
	for i := range txs {
		amount += txs[i].Amount
		r[i] = *txs[i].ID
	}
	return r, amount, nil
}

func getExtraFee(ctx context.Context, cl *client.Client, addr proto.Address) (uint64, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/addresses/scriptInfo/%s", cl.GetOptions().BaseUrl, addr.String()), nil)
	if err != nil {
		return 0, err
	}
	extraFee := new(AddressesExtraFee)
	r, err := cl.Do(ctx, req, extraFee)
	if err != nil {
		return 0, err
	}
	if r.StatusCode != http.StatusOK {
		return 0, errors.New("failed to get extra fee")
	}
	return extraFee.ExtraFee, nil
}

func nodeClient(ctx context.Context, s string) (*client.Client, error) {
	var u *url.URL
	var err error
	if strings.Contains(s, "//") {
		u, err = url.Parse(s)
	} else {
		u, err = url.Parse("//" + s)
	}
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		u.Scheme = defaultScheme
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme '%s'", u.Scheme)
	}
	cl, err := client.NewClient(client.Options{BaseUrl: u.String(), Client: &http.Client{}})
	if err != nil {
		return nil, err
	}
	_, _, err = cl.Blocks.Height(ctx)
	if err != nil {
		return nil, err
	}
	return cl, nil
}

func getScheme(ctx context.Context, cl *client.Client) (proto.Scheme, error) {
	b, _, err := cl.Blocks.Last(ctx)
	if err != nil {
		return 0, err
	}
	return b.Generator.Bytes()[1], nil
}

func showUsage() {
	_, _ = fmt.Fprintf(os.Stderr, "\nUsage of Waves Automatic Lessor %s\n", version)
	flag.PrintDefaults()
}

func parseSK(scheme proto.Scheme, s string) (crypto.SecretKey, crypto.PublicKey, proto.Address, error) {
	sk, err := crypto.NewSecretKeyFromBase58(s)
	if err != nil {
		return crypto.SecretKey{}, crypto.PublicKey{}, proto.Address{}, err
	}
	pk := crypto.GeneratePublicKey(sk)
	address, err := proto.NewAddressFromPublicKey(scheme, pk)
	if err != nil {
		return crypto.SecretKey{}, crypto.PublicKey{}, proto.Address{}, err
	}
	return sk, pk, address, nil
}
