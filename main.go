//
//  TODO:
//
//    * Add sub-commands for different modes.  We want at least two:
//
//        ps serve  -> Run the httpd.
//        ps prune  -> Remove old reports
//
//    * Update the SQLite magic.
//       - Simplify how this works.
//       - Add command-line flags for storage-location and DB-path.
//
//
// Steve
// --
//

package main

import (
	"flag"
	"fmt"
	"github.com/gorilla/mux"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"text/template"
)

/*
 * Handle the submission of Puppet report.
 *
 * The input is read, and parsed as Yaml, and assuming that succeeds
 * then the data is written beneath ./reports/$hostname/$timestamp
 * and a summary-record is inserted into our SQLite database.
 *
 */
func ReportSubmissionHandler(res http.ResponseWriter, req *http.Request) {
	var (
		status int
		err    error
	)
	defer func() {
		if nil != err {
			http.Error(res, err.Error(), status)
		}
	}()

	//
	// Read the body of the request.
	//
	content, err := ioutil.ReadAll(req.Body)
	if err != nil {
		status = http.StatusInternalServerError
		return
	}

	//
	// Parse the YAML into something we can work with.
	//
	report, err := ParsePuppetReport(content)
	if err != nil {
		status = http.StatusInternalServerError
		return
	}

	//
	// Create a report directory, unless it already exists.
	//
	dir := filepath.Join("./reports", report.Fqdn)
	_, err = os.Stat(dir)
	if err != nil {
		err := os.MkdirAll(dir, 0755)
		if err != nil {
			status = http.StatusInternalServerError
			return
		}
	}

	//
	// Now write out the file.
	//
	path := filepath.Join(dir, fmt.Sprintf("%d", report.At_Unix))
	err = ioutil.WriteFile(path, content, 0644)
	if err != nil {
		status = http.StatusInternalServerError
		return
	}

	//
	// Record the new entry in our SQLite database
	//
	addDB(report, path)

	//
	// Show something to the caller.
	//
	out := fmt.Sprintf("{\"host\":\"%s\"}", report.Fqdn)
	fmt.Fprintf(res, string(out))

}

//
// Called via GET /report/NN
//
func ReportHandler(res http.ResponseWriter, req *http.Request) {
	var (
		status int
		err    error
	)
	defer func() {
		if nil != err {
			http.Error(res, err.Error(), status)
		}
	}()

	//
	// Get the node name we're going to show.
	//
	vars := mux.Vars(req)
	id := vars["id"]

	//
	// Get the content.
	//
	content, err := getYAML(id)
	if err != nil {
		status = http.StatusInternalServerError
		return
	}

	//
	// Parse it
	//
	report, err := ParsePuppetReport(content)
	if err != nil {
		status = http.StatusInternalServerError
		return
	}

	//
	// Define a template for the result we'll send to the browser.
	//
	tmpl, err := Asset("data/report_handler.template")
	if err != nil {
		fmt.Printf("Failed to find asset data/report_handler.template")
		os.Exit(2)
	}

	//
	// All done.
	//
	src := string(tmpl)
	t := template.Must(template.New("tmpl").Parse(src))
	t.Execute(res, report)
}

//
// Called via GET /node/$FQDN
//
func NodeHandler(res http.ResponseWriter, req *http.Request) {
	var (
		status int
		err    error
	)
	defer func() {
		if nil != err {
			http.Error(res, err.Error(), status)
		}
	}()

	//
	// Get the node name we're going to show.
	//
	vars := mux.Vars(req)
	fqdn := vars["fqdn"]

	//
	// Get the reports
	//
	reports, err := getReports(fqdn)

	if (reports == nil) || (len(reports) < 1) {
		status = http.StatusNotFound
		return
	}

	//
	// Annoying struct to allow us to populate our template
	// with both the reports and the fqdn of the host.
	//
	type Pagedata struct {
		Fqdn  string
		Nodes []PuppetReportSummary
	}

	//
	// Populate this structure.
	//
	var x Pagedata
	x.Nodes = reports
	x.Fqdn = fqdn

	//
	// Define a template for the result we'll send to the browser.
	//
	tmpl, err := Asset("data/node_handler.template")
	if err != nil {
		fmt.Printf("Failed to find asset data/node_handler.template")
		os.Exit(2)
	}

	//
	//  Populate the template and return it.
	//
	src := string(tmpl)
	t := template.Must(template.New("tmpl").Parse(src))
	t.Execute(res, x)
}

//
// Show the hosts we know about - and their last known state.
//
func IndexHandler(res http.ResponseWriter, req *http.Request) {

	//
	// Define a template for the result we'll send to the browser.
	//
	tmpl, err := Asset("data/index_handler.template")
	if err != nil {
		fmt.Printf("Failed to find asset data/index_handler.template")
		os.Exit(2)
	}

	//
	// Get the nodes to show on our front-page
	//
	NodeList, err := getIndexNodes()
	if err != nil {
		panic(err)
	}

	//
	//  Populate the template and return it.
	//
	src := string(tmpl)
	t := template.Must(template.New("tmpl").Parse(src))
	t.Execute(res, NodeList)
}

//
//  Entry-point.
//
func main() {

	//
	// Parse the command-line arguments.
	//
	host := flag.String("host", "127.0.0.1", "The IP to listen upon")
	port := flag.Int("port", 3001, "The port to bind upon")
	db   := flag.String("db-file", "foo.db", "The SQLite database to use")
	flag.Parse()

	SetupDB( *db )

	//
	// Create a new router and our route-mappings.
	//
	router := mux.NewRouter()

	//
	// Upload a new report.
	//
	router.HandleFunc("/upload", ReportSubmissionHandler).Methods("POST")

	//
	// Show the recent state of a node.
	//
	router.HandleFunc("/node/{fqdn}", NodeHandler).Methods("GET")

	//
	// Show "everything" about a given run.
	//
	router.HandleFunc("/report/{id}", ReportHandler).Methods("GET")

	//
	// Handle a display of all known nodes, and their last state.
	//
	router.HandleFunc("/", IndexHandler).Methods("GET")

	//
	// Bind the router.
	//
	http.Handle("/", router)

	//
	// Launch the server
	//
	fmt.Printf("Launching the server on http://%s:%d\n", *host, *port)
	fmt.Printf("SQLIte file is %s\n", *db)
	err := http.ListenAndServe(fmt.Sprintf("%s:%d", *host, *port), nil)
	if err != nil {
		panic(err)
	}
}
