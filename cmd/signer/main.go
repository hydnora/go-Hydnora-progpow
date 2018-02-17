// Copyright 2017 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

// signer is a utility that can be used so sign transactions and
// arbitrary data.
package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"context"
	"encoding/json"

	"github.com/ethereum/go-ethereum/cmd/signer/core"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/rpc"
	"gopkg.in/urfave/cli.v1"
)

// EXT_API_VERSION -- see extapi_changelog.md
const EXT_API_VERSION = "2.0.0"

// INT_API_VERSION -- see intapi_changelog.md
const INT_API_VERSION = "1.2.0"

func main() {

	app := cli.NewApp()
	app.Name = "signer"
	app.Usage = "Manage ethereum Account operations"
	app.Flags = []cli.Flag{
		cli.IntFlag{
			Name:  "loglevel",
			Value: 4,
			Usage: "log level to emit to the screen",
		},
		cli.StringFlag{
			Name:  "keystore",
			Value: filepath.Join(node.DefaultDataDir(), "keystore"),
			Usage: "Directory for the keystore",
		},
		utils.NetworkIdFlag,
		utils.LightKDFFlag,
		utils.NoUSBFlag,
		utils.RPCListenAddrFlag,
		cli.IntFlag{
			Name:  "rpcport",
			Usage: "HTTP-RPC server listening port",
			Value: node.DefaultHTTPPort + 5,
		},
		cli.StringFlag{
			Name:  "4bytedb",
			Usage: "File containing 4byte-identifiers",
			Value: "./4byte.json",
		},
		cli.StringFlag{
			Name:  "4bytedb-custom",
			Usage: "File used for writing new 4byte-identifiers submitted via API",
			Value: "./4byte-custom.json",
		},
		cli.StringFlag{
			Name:  "auditlog",
			Usage: "File used to emit audit logs. Set to \"\" to disable",
			Value: "audit.log",
		},
		cli.StringFlag{
			Name:  "requestfile",
			Usage: "File containing requests to handle",
			Value: "",
		},
		cli.BoolFlag{
			Name: "stdio-ui",
			Usage: "Use STDIN/STDOUT as a channel for an external UI. " +
				"This means that an STDIN/STDOUT is used for RPC-communication with a e.g. a graphical user " +
				"interface, and can be used when the signer is started by an external process.",
		},
		cli.BoolFlag{
			Name:  "stdio-ui-test",
			Usage: "Mechanism to test interface between signer and UI. Requires 'stdio-ui'.",
		},
	}

	app.Action = func(c *cli.Context) error {

		var (
			ui core.SignerUI
		)
		// Set up the logger to print everything
		logOutput := os.Stdout
		if c.Bool("stdio-ui") {
			logOutput = os.Stderr
		}
		log.Root().SetHandler(log.LvlFilterHandler(log.Lvl(c.Int("loglevel")), log.StreamHandler(logOutput, log.TerminalFormat(true))))

		if c.Bool("stdio-ui") {
			ui = core.NewStdIOUI()
		} else {
			ui = core.NewCommandlineUI()
		}
		if c.Bool("stdio-ui") {
			log.Info("Using stdin/stdout as UI-channel")
		}
		db, err := core.NewAbiDBFromFiles(c.String("4bytedb"), c.String("4bytedb-custom"))

		if err != nil {
			utils.Fatalf(err.Error())
		}
		log.Info("Loaded 4byte db", "signatures", db.Size(), "file", c.String("4bytedb"))

		var (
			api      core.ExternalAPI
			listener net.Listener
			server   = rpc.NewServer()
		)

		api_impl := core.NewSignerAPI(
			c.Int64(utils.NetworkIdFlag.Name),
			c.String("keystore"),
			c.Bool(utils.NoUSBFlag.Name),
			ui, db,
			c.Bool(utils.LightKDFFlag.Name))

		api = api_impl

		// Audit logging
		if logfile := c.String("auditlog"); logfile != "" {
			api, err = core.NewAuditLogger(logfile, api_impl)
			if err != nil {
				utils.Fatalf(err.Error())
			}
			log.Info("Audit logs configured", "file", logfile)
		}
		// register signer API with server
		if err = server.RegisterName("account", api); err != nil {
			utils.Fatalf("Could not register signer API: %v", err)
		}

		// Import from file
		if rfile := c.String("requestfile"); rfile != "" {
			//Each line of file represents one request
			log.Warn("Import from file not yet implemented")
		}

		// start http server
		endpoint := fmt.Sprintf("%s:%d", c.String(utils.RPCListenAddrFlag.Name), c.Int("rpcport"))
		if listener, err = net.Listen("tcp", endpoint); err != nil {
			utils.Fatalf("Could not start http listener: %v", err)
		}
		extapi_url := fmt.Sprintf("http://%s", endpoint)
		log.Info("HTTP endpoint opened", "url", extapi_url)
		cors := []string{"*"}

		if c.Bool("stdio-ui-test") {
			log.Info("Performing UI test")
			go testExternalUI(api_impl)
		}
		ui.OnSignerStartup(core.StartupInfo{
			Info: map[string]interface{}{
				"extapi_version": EXT_API_VERSION,
				"intapi_version": INT_API_VERSION,
				"extapi_http":    extapi_url,
				"extapi_ipc":     nil,
			},
		})

		rpc.NewHTTPServer(cors, server).Serve(listener)

		return nil
	}
	app.Run(os.Args)

}

func testExternalUI(api *core.SignerAPI) {

	ctx := context.WithValue(context.Background(), "remote", "signer binary")
	ctx = context.WithValue(ctx, "scheme", "in-proc")
	ctx = context.WithValue(ctx, "local", "main")

	errs := make([]string, 0)

	api.UI.ShowInfo("Testing 'ShowInfo'")
	api.UI.ShowError("Testing 'ShowError'")

	checkErr := func(method string, err error) {
		if err != nil && err != core.ErrRequestDenied {
			errs = append(errs, fmt.Sprintf("%v: %v", method, err.Error()))
		}
	}
	var err error

	_, err = api.SignTransaction(ctx, core.SendTxArgs{From: common.MixedcaseAddress{}}, nil)
	checkErr("SignTransaction", err)
	_, err = api.Sign(ctx, common.MixedcaseAddress{}, common.Hex2Bytes("01020304"))
	checkErr("Sign", err)
	_, err = api.List(ctx)
	checkErr("List", err)
	_, err = api.New(ctx)
	checkErr("New", err)
	_, err = api.Export(ctx, common.Address{})
	checkErr("Export", err)
	_, err = api.Import(ctx, json.RawMessage{})
	checkErr("Import", err)

	api.UI.ShowInfo("Tests completed")

	if len(errs) > 0 {
		log.Error("Got errors")
		for _, e := range errs {
			log.Error(e)
		}
	} else {
		log.Info("No errors")
	}

}

/**
//Create Account

curl -H "Content-Type: application/json" -X POST --data '{"jsonrpc":"2.0","method":"account_new","params":["test"],"id":67}' localhost:8550

// List accounts

curl -i -H "Content-Type: application/json" -X POST --data '{"jsonrpc":"2.0","method":"account_list","params":[""],"id":67}' http://localhost:8550/

// Make Transaction
// safeSend(0x12)
// 4401a6e40000000000000000000000000000000000000000000000000000000000000012

// supplied abi
curl -i -H "Content-Type: application/json" -X POST --data '{"jsonrpc":"2.0","method":"account_signTransaction","params":["0x82A2A876D39022B3019932D30Cd9c97ad5616813",{"gas":"0x333","gasPrice":"0x123","nonce":"0x0","to":"0x07a565b7ed7d7a678680a4c162885bedbb695fe0", "value":"0x10", "data":"0x4401a6e40000000000000000000000000000000000000000000000000000000000000012"},"test"],"id":67}' http://localhost:8550/

// Not supplied
curl -i -H "Content-Type: application/json" -X POST --data '{"jsonrpc":"2.0","method":"account_signTransaction","params":["0x82A2A876D39022B3019932D30Cd9c97ad5616813",{"gas":"0x333","gasPrice":"0x123","nonce":"0x0","to":"0x07a565b7ed7d7a678680a4c162885bedbb695fe0", "value":"0x10", "data":"0x4401a6e40000000000000000000000000000000000000000000000000000000000000012"}],"id":67}' http://localhost:8550/

**/
