package cinc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	cinc "github.com/cinc-project/cinc-api"
)

// client stands in for a *cinc.Client built as in ExampleNewClient. The
// examples that talk to a server are compiled but not run, since there is no
// server to run them against; the others run as tests and check their output.
var client *cinc.Client

func ExampleNewClient() {
	key, err := cinc.LoadKeyFile("/etc/chef/client.pem")
	if err != nil {
		log.Fatal(err)
	}
	c, err := cinc.NewClient(cinc.Config{
		ServerURL:  "https://chef.example.com",
		Org:        "acme",
		ClientName: "node1",
		Key:        key,
	}, cinc.WithUserAgent("my-tool/1.0"), cinc.WithMaxRetries(3))
	if err != nil {
		log.Fatal(err)
	}
	node, _, err := c.Nodes.Get(context.Background(), "web01")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(node.Environment)
}

// A knife-style URL holds the server and the organization together;
// ParseServerURL splits it into the two values Config takes.
func ExampleParseServerURL() {
	serverURL, org, err := cinc.ParseServerURL("https://chef.example.com/organizations/acme")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(serverURL)
	fmt.Println(org)
	fmt.Println(cinc.FormatServerURL(serverURL, org))
	// Output:
	// https://chef.example.com
	// acme
	// https://chef.example.com/organizations/acme
}

// Every non-2xx response is an *ErrorResponse that also matches a sentinel
// error, so the common cases need no status-code checks.
func ExampleErrorResponse() {
	_, _, err := client.Nodes.Get(context.Background(), "web01")
	var er *cinc.ErrorResponse
	switch {
	case errors.Is(err, cinc.ErrNotFound):
		fmt.Println("no such node")
	case errors.Is(err, cinc.ErrForbidden):
		fmt.Println("not allowed")
	case errors.As(err, &er):
		fmt.Println(er.StatusCode, er.ServerMessage())
	case err != nil:
		log.Fatal(err)
	}
}

// Modify reads the node, applies the change and writes it back only if the
// change did something.
func ExampleNodesService_Modify() {
	_, changed, err := client.Nodes.Modify(context.Background(), "web01", func(n *cinc.Node) error {
		n.AddTags("canary")
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("changed:", changed)
}

// All pages through every result, one page in memory at a time.
func ExampleSearchService_All() {
	ctx := context.Background()
	for row, err := range client.Search.All(ctx, "node", "role:web", cinc.WithRows(500)) {
		if err != nil {
			log.Fatal(err)
		}
		var n struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(cinc.UnwrapSearchRow(row), &n); err != nil {
			log.Fatal(err)
		}
		fmt.Println(n.Name)
	}
}

// Nodes decodes each row; a partial search fetches only the paths asked for.
func ExampleSearchService_Nodes() {
	ctx := context.Background()
	for node, err := range client.Search.Nodes(ctx, "platform:ubuntu") {
		if err != nil {
			log.Fatal(err)
		}
		if t, ok := node.LastCheckin(); ok && time.Since(t) > 24*time.Hour {
			fmt.Println(node.Name, "has not checked in for a day")
		}
	}
}

// Numbers in attributes decode as json.Number, so large integers survive a
// read-modify-write unrounded.
func ExampleAttributes_Dig() {
	var node cinc.Node
	err := json.Unmarshal([]byte(`{"name":"web01","automatic":{"kernel":{"release":"6.8.0"},"fs":{"inodes":9007199254740993}}}`), &node)
	if err != nil {
		log.Fatal(err)
	}
	release, _ := node.Automatic.Dig("kernel", "release")
	inodes, _ := node.Automatic.Dig("fs", "inodes")
	fmt.Println(release)
	fmt.Println(inodes)
	// Output:
	// 6.8.0
	// 9007199254740993
}

func ExampleNormalizeRunList() {
	fmt.Println(cinc.NormalizeRunList([]string{"base", "role[web]", "recipe[base]", "nginx::default"}))
	// Output: [recipe[base] role[web] recipe[nginx::default]]
}

func ExampleCompareCookbookVersions() {
	fmt.Println(cinc.CompareCookbookVersions("10.0.0", "9.1.0") > 0)
	fmt.Println(cinc.CompareCookbookVersions("1.2", "1.2.0") == 0)
	// Output:
	// true
	// true
}

// Encrypt writes Chef's version 3 (AES-256-GCM) format; Decrypt also reads
// versions 1 and 2. The id stays in clear text.
func ExampleDataBagItem_Encrypt() {
	secret := []byte("correct horse battery staple")
	item := cinc.DataBagItem{"id": "db", "password": "hunter2"}

	enc, err := item.Encrypt(secret)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(enc.ID(), enc.IsEncrypted())

	dec, err := enc.Decrypt(secret)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(dec["password"])
	// Output:
	// db true
	// hunter2
}

func ExampleDataBagItemsService_GetDecrypted() {
	secret, err := cinc.LoadDataBagSecret("/etc/chef/encrypted_data_bag_secret")
	if err != nil {
		log.Fatal(err)
	}
	item, _, err := client.DataBags.Items("credentials").GetDecrypted(context.Background(), "db", secret)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(item["password"])
}

// Upload runs the sandbox flow: it uploads only the files the server lacks,
// then commits the version's manifest.
func ExampleCookbooksService_Upload() {
	cb, err := cinc.LocalCookbookFromDir("cookbooks/nginx", "") // version from metadata
	if err != nil {
		log.Fatal(err)
	}
	err = client.Cookbooks.Upload(context.Background(), cb, cinc.WithFreeze())
	if errors.Is(err, cinc.ErrCookbookFrozen) {
		fmt.Println(cb.Version, "is already frozen")
	} else if err != nil {
		log.Fatal(err)
	}
}

// PushRevision is the server half of `chef push`: it uploads the cookbook
// artifacts the server lacks and associates the lock with a policy group.
// MissingArtifacts says which cookbooks need to be on disk.
func ExamplePoliciesService_PushRevision() {
	ctx := context.Background()
	lock, raw, err := cinc.LoadPolicyfileLock("Policyfile.lock.json")
	if err != nil {
		log.Fatal(err)
	}
	missing, _, err := client.Policies.MissingArtifacts(ctx, lock)
	if err != nil {
		log.Fatal(err)
	}
	cookbooks := map[string]*cinc.LocalCookbook{}
	for _, name := range missing {
		cb, err := cinc.LocalCookbookFromDir("cookbooks/"+name, lock.CookbookLocks[name].Version)
		if err != nil {
			log.Fatal(err)
		}
		cookbooks[name] = cb
	}
	res, _, err := client.Policies.PushRevision(ctx, raw, "production", cookbooks)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("uploaded:", res.Uploaded, "already present:", res.AlreadyPresent)
}

// Grant reads the ACL once and writes only the permissions that change.
func ExampleACLsService_Grant() {
	changed, err := client.ACLs.Grant(context.Background(),
		cinc.ObjectACL(cinc.ACLNodes, "web01"), "all",
		cinc.ACLMembers{Groups: []string{"ops"}, Clients: []string{"deployer"}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("changed:", changed)
}

// ExpandPerm turns "all" into the five permissions Chef stores.
func ExampleExpandPerm() {
	perms, err := cinc.ExpandPerm("all")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(perms)
	// Output: [create read update delete grant]
}

// Do sends a signed request and returns the body byte for byte, for callers
// such as backup tools that must not lose anything to a decode.
func ExampleClient_Do() {
	body, _, err := client.Do(context.Background(), "GET", "/organizations/acme/nodes/web01", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s\n", body)
}
