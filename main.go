package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
)

type options struct {
	outputFormat string
	valueType    string
}

func makeDefaultOptions() *options {
	return &options{
		outputFormat: "plain",
		valueType:    "single",
	}
}

func output(options *options, sink io.Writer, values []string) error {
	if options.valueType == "single" && len(values) != 1 {
		return fmt.Errorf("expected 1 value but got %d (%v)", len(values), values)
	}
	switch options.outputFormat {
	case "plain":
		for _, record := range values {
			if _, err := fmt.Fprintln(sink, record); err != nil {
				return err
			}
		}
	case "json":
		var err error
		encoder := json.NewEncoder(sink)
		encoder.SetEscapeHTML(false)
		switch options.valueType {
		case "single":
			err = encoder.Encode(values[0])
		case "list":
			err = encoder.Encode(values)
		}
		if err != nil {
			return errors.Wrap(err, "error writing JSON")
		}
	}
	return nil
}

func getTxtRecords(options *options, domain string) ([]string, error) {
	if !strings.HasSuffix(domain, ".") {
		domain = domain + "."
	}
	// TODO: just pulling the config from /etc/resolv.conf works most of the time, but we really need options for the other times
	config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return nil, errors.Wrap(err, "error configuring DNS client")
	}
	// This DNS client library doesn't do retries or fallbacks to TCP, but it's good enough for a proof of concept.
	// Maybe replace all this with bindings to one of the more solid C libraries.
	client := new(dns.Client)
	query := new(dns.Msg)
	query.SetQuestion(domain, dns.TypeTXT)
	query.RecursionDesired = true

	response, _, err := client.Exchange(query, config.Servers[0]+":"+config.Port)
	if err != nil {
		return nil, errors.Wrap(err, "error executing DNS query")
	}

	switch response.Rcode {
	case dns.RcodeSuccess:
		// okay

	case dns.RcodeNameError: // a.k.a. NXDOMAIN
		// TODO: add an option to allow ignoring this
		// This is the default for safety reasons
		return nil, errors.Errorf("no TXT records for domain %s", domain)

	default:
		return nil, errors.Errorf("error from remote DNS server: %s", dns.RcodeToString[response.Rcode])
	}

	var results []string
	for _, answer := range response.Answer {
		if txt, ok := answer.(*dns.TXT); ok {
			results = append(results, strings.Join(txt.Txt, ""))
		}
	}
	return results, nil
}

func lookUpValues(options *options, txtRecords []string, key string, defaultValues []string) ([]string, error) {
	var values []string
	for _, record := range txtRecords {
		pieces := strings.SplitN(record, "=", 2)
		if len(pieces) > 1 && pieces[0] == key {
			values = append(values, pieces[1])
		}
	}
	if len(values) == 0 {
		values = defaultValues
	}

	if options.valueType == "single" {
		if len(values) == 0 {
			return nil, errors.Errorf("no values found for key %s, and no default provided", key)
		}
		if len(values) > 1 {
			return nil, errors.Errorf("%d values found for key %s, but only 1 was expected", len(values), key)
		}
	}

	return values, nil
}

func main() {
	options := makeDefaultOptions()
	kingpin.CommandLine.HelpFlag.Short('h')
	kingpin.Flag("format", "Output format (plain, json)").Short('f').Default("plain").EnumVar(&options.outputFormat, "plain", "json")
	kingpin.Flag("type", "Data value type (single, list)").Short('t').Default("single").EnumVar(&options.valueType, "single", "list")
	domain := kingpin.Arg("domain", "Domain name to query for TXT records").Required().String()
	key := kingpin.Arg("key", "Key name to look up in domain").Required().String()
	defaultValues := kingpin.Arg("default", "Default value(s) to use if key is not found").Strings()
	kingpin.Parse()

	if *defaultValues == nil {
		defaultValues = &[]string{}
	}

	if options.valueType == "single" && len(*defaultValues) > 1 {
		fmt.Fprintf(os.Stderr, "Got %n default values, but the value type is \"single\".  (Did you mean to set --type list?)", len(*defaultValues))
		os.Exit(1)
	}

	txtRecords, err := getTxtRecords(options, *domain)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error doing DNS lookup:\n%+v\n", err.Error())
		os.Exit(3)
	}

	var values []string
	values, err = lookUpValues(options, txtRecords, *key, *defaultValues)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error looking up values for key \"%s\" in domain %s:\n%+v\n", *key, *domain, err.Error())
		os.Exit(4)
	}

	if err = output(options, os.Stdout, values); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output values: %s", err.Error())
		os.Exit(5)
	}
}
