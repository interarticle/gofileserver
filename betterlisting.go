package main

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
)

const betterListingTemplate = `
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no">
<link rel="stylesheet" href="https://maxcdn.bootstrapcdn.com/bootstrap/4.0.0-beta.2/css/bootstrap.min.css" integrity="sha384-PsH8R72JQ3SOdhVi3uxftmaW6Vc51MKb0q5P2rRUpPvrszuE4W1povHYgTpBfshb" crossorigin="anonymous">
<style>
th.sorting-up::after {
    content: " ↑";
}
th.sorting-down::after {
    content: " ↓";
}
.sortable th {
    cursor: hand;
}
</style>
<div class="container">
    <h1>Listing for {{.Path}}</h1>

    <table id="listing" class="table sortable">
        <thead class="thead-dark">
            <tr>
                <th>Name</th>
                <th>Mod Time</th>
                <th>Size</th>
            </tr>
        </thead>
        <tbody>
            {{range .Dirs}}
            <tr>
                <td>
                    <a href="{{.Name}}{{if .IsDir}}/{{end}}">
                        {{.Name}}{{if .IsDir}}/{{end}}
                    </a>
                </td>
                <td data-unix-sec="{{.ModTime.Unix}}" data-nanos="{{.ModTime.Nanosecond}}" class="compute-date" title="{{.ModTime}}">{{.ModTime}}</td>
                <td data-sort-num="{{.Size}}" title="{{.Size}}">{{ByteSize .Size}}</td>
            </tr>
            {{end}}
        </tbody>
    </table>
</div>

<script src="https://ajax.googleapis.com/ajax/libs/jquery/3.2.1/jquery.min.js"></script>
<script>
    const table = $("#listing");
    const tbody = table.find("tbody");
    const headers = table.find("thead>tr>th");
    let index = 0;
    for (const h of headers) {
        const thisIndex = index;
        const header = $(h);
        header.click(() => {
            if (header.hasClass("sorting-up")) {
                location.hash = 'sort=d' + thisIndex;
            } else {
                location.hash = 'sort=a' + thisIndex;
            }
        });
        index++;
    }
    function clearSort() {
        headers.each(function() {
            $(this).removeClass("sorting-up");
            $(this).removeClass("sorting-down");
        });
    }
    function onHashChange() {
        const hashParams = location.hash
            .replace(/^#/, '')
            .split('&')
            .map(kv => kv.split('=', 2))
            .reduce((dict, pair) => {
                dict[decodeURIComponent(pair[0])] = decodeURIComponent(pair[1]);
                return dict;
            }, {});
        if (!hashParams['sort']) return;
        const match = /^([ad])(\d+)$/.exec(hashParams['sort']);
        if (!match) return;
        const sortDirection = match[1] == 'a' ? 1 : -1;
        const thisIndex = parseInt(match[2]);
        if (thisIndex < 0 || thisIndex >= headers.length) return;
        const header = $(headers[thisIndex]);

        const rows = tbody.find("tr");
        rows.sort((a, b) => {
            const aElem = $($(a).children()[thisIndex]);
            const bElem = $($(b).children()[thisIndex]);
            const aVal = aElem.data("sort-num") && parseFloat(aElem.data("sort-num")) || aElem.text();
            const bVal = bElem.data("sort-num") && parseFloat(bElem.data("sort-num")) || bElem.text();
            if (aVal > bVal) {
                return sortDirection;
            } else if (aVal < bVal) {
                return -sortDirection;
            } else {
                return 0;
            }
        });
        rows.detach().appendTo(tbody);
        clearSort();
        switch (sortDirection) {
            case 1:
                header.addClass("sorting-up");
                break;
            case -1:
                header.addClass("sorting-down");
                break;
        }
    }
    $(window).bind("hashchange", onHashChange);
    $(".compute-date").each(function() {
        const date = new Date(parseInt($(this).data("unix-sec")) * 1000 + parseInt($(this).data("nanos")) / 1000000);
        $(this).data("sort-num", date.getTime());
    });
    onHashChange();
</script>
`

var (
	errWrongType    = errors.New("Wrong path type")
	listingTemplate *template.Template
)

var byteUnits = []string{"B", "KiB", "MiB", "GiB", "TiB"}

func byteSize(b int64) string {
	size := float64(b)
	var i int
	for i = 0; i < len(byteUnits)-1 && size > 1000; i++ {
		size /= 1024
	}
	return fmt.Sprintf("%.2f %s", size, byteUnits[i])
}

func init() {
	listingTemplate = template.Must(template.New("template").Funcs(template.FuncMap{
		"ByteSize": byteSize,
	}).Parse(betterListingTemplate))
}

type betterHttpListingServer struct {
	root       http.FileSystem
	fileServer http.Handler
}

func newBetterHttpListingServer(root http.FileSystem) http.Handler {
	return &betterHttpListingServer{
		root:       root,
		fileServer: http.FileServer(root),
	}
}

func (s *betterHttpListingServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := path.Clean(r.URL.Path)
	if !strings.HasSuffix(r.URL.Path, "/") {
		s.fileServer.ServeHTTP(w, r)
		return
	}

	err := s.tryHandle(w, r, p)
	if err != nil {
		if err == errWrongType {
			s.fileServer.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
	}
}

func (s *betterHttpListingServer) tryHandle(w http.ResponseWriter, r *http.Request, p string) error {
	f, err := s.root.Open(p)
	if err != nil {
		return err
	}

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	if !stat.IsDir() {
		return errWrongType
	}

	dirs, err := f.Readdir(-1)
	if err != nil {
		return err
	}

	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].Name() < dirs[j].Name()
	})

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	err = listingTemplate.Execute(w, struct {
		Path string
		Dirs []os.FileInfo
	}{
		p,
		dirs,
	})
	if err != nil {
		return err
	}

	return nil
}
