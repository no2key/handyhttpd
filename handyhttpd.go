package main

import (
    "flag"
    "log"
    "io"
    "os"
    "os/signal"
    "path/filepath"
    "net"
    "net/url"
    "net/http"
    "syscall"
    "fmt"
    "strconv"

    h "./handy"
)

const (
    HANDY_SOCK_FILENAME = "handyhttpd.sock"
    HANDY_LOG_FILENAME = "handyhttpd.log"
)

// remember all handler instances
var (
    gLogger *log.Logger
)

func parseParams(root, pattern string, port int, remove bool) bool {
    handy, ok := h.Find(port)

    if !ok {
        if handy = h.New(port, gLogger); handy == nil {
            gLogger.Println("cannot create new handy server")
            return false
        }
    }

    if remove {
        handy.Del(root)
    } else {
        handy.Add(root, pattern)
    }

    handy.Start()
    return true
}

func main() {
    port := flag.Int("port", 0, "Port to serve http request. By default, the port is the last port you've used.")
    dir := flag.String("dir", "", "Dir served as www root. By default, current dir will be served.")
    alias := flag.String("alias", "", "URL alias to serve. By default, dir name will be used as alias.")
    remove := flag.Bool("remove", false, "Remove current dir so that no one can visit it thru http anymore.")
    list := flag.Bool("list", false, "List all running servers and hosted dirs.")
    quit := flag.Bool("quit", false, "Quit server completely.")
    flag.Parse()

    tempdir := os.TempDir()
    file, err := os.OpenFile(tempdir + "/" + HANDY_LOG_FILENAME, os.O_APPEND | os.O_CREATE | os.O_RDWR, 0666)
    if err != nil {
        log.Println("cannot open log file to write. filename:", tempdir + "/" + HANDY_LOG_FILENAME, "err:", err)
        log.Println("print log to stdout now")
        file = os.Stdout
    }

    gLogger = log.New(file, "", log.LstdFlags)
    gLogger.Printf("parsed params. [port: %d] [dir: %s] [alias: %s] [remove: %t] [list: %t] [quit: %t]\n",
        *port, *dir, *alias, *remove, *list, *quit)

    root := *dir
    if root == "" {
        root, _ = os.Getwd()
    }

    pattern := *alias
    if pattern == "" {
        pattern = filepath.Base(root)
    }

    socket := tempdir + "/" + HANDY_SOCK_FILENAME
    l, err := net.Listen("unix", socket)

    // there is a handyhttpd running. notify it with current options.
    if err != nil {
        // hack http client's transport to force it to use unix socket rather than tcp
        client := http.Client {
            Transport: &http.Transport {
                Dial: func (n, addr string) (conn net.Conn, err error) {
                    return net.Dial("unix", socket)
                },
            },
        }

        var r *http.Response

        if *list {
            r, err = client.Get("http://localhost/list")
        } else if *quit {
            r, err = client.Get("http://localhost/quit")
        } else {
            var verb string
            if *remove {
                verb = "remove"
            } else {
                verb = "add"
            }

            // format is:
            // GET /?verb=add&alias=abc&dir=/path/to/www/root&port=9696
            r, err = client.Get(
                fmt.Sprintf("http://localhost/?verb=%s&alias=%s&dir=%s&port=%d",
                    verb, url.QueryEscape(pattern), url.QueryEscape(root), *port))
        }

        if err != nil {
            gLogger.Println("cannot connect handy server. err:", err)
            return
        }

        if r.StatusCode != 200 {
            gLogger.Println("handy server denies the request. code:", r.StatusCode)
            return
        }

        io.Copy(os.Stdout, r.Body)
        return
    }

    // as this server is the only running server, nothing to list
    if *list {
        fmt.Println("No server is running")
        l.Close()
        return
    }

    parseParams(root, pattern, *port, *remove)

    go func() {
        // handle other server's request
        http.HandleFunc("/list", func(w http.ResponseWriter, r *http.Request) {
            gLogger.Println("listing all ports per request")
            h.List(w)
        })
        http.HandleFunc("/quit", func(w http.ResponseWriter, r *http.Request) {
            fmt.Fprintln(w, "Handy server is quiting now")

            // gracefully exit
            process, _ := os.FindProcess(os.Getpid())
            process.Signal(syscall.SIGINT)
        })
        http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
            gLogger.Println("handle a verb request. url:", r.URL)
            r.ParseForm()
            verb, verbOk := r.Form["verb"]
            alias, aliasOk := r.Form["alias"]
            dir, dirOk := r.Form["dir"]
            port, portOk := r.Form["port"]

            if !verbOk || !aliasOk || !dirOk || !portOk {
                gLogger.Println("missing required query string params")
                w.WriteHeader(http.StatusBadRequest)
                return
            }

            remove := false
            if verb[0] == "remove" {
                remove = true
            }

            portNumber, _ := strconv.Atoi(port[0])
            if parseParams(dir[0], alias[0], portNumber, remove) {
                fmt.Fprintf(w, "%s dir %s as /%s on port %d\n", verb[0], dir[0], alias[0], h.LastPort())
            }
        })
        http.Serve(l, nil)
    }()

    defer l.Close()
    defer h.Stop()
    sig := make(chan os.Signal)
    signal.Notify(sig)

    for {
        s := <-sig
        if s == syscall.SIGKILL || s == syscall.SIGINT || s == syscall.SIGTERM {
            gLogger.Println("gracefully exit with signal", s)
            return
        }
    }
}
