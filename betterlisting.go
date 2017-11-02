package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path"
	"path/filepath"
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
                <th class="no-sort"></th>
                <th>Name</th>
                <th>Mod Time</th>
                <th>Size</th>
            </tr>
        </thead>
        <tbody>
            {{range .Dirs}}
            <tr data-hash-key="{{.PathHashString}}">
                <td class="states">
                    <input class="checked" type="checkbox" disabled>
                </td>
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
    let lastHash = '';
    let index = 0;
    for (const h of headers) {
        const thisIndex = index;
        const header = $(h);
        if (!header.hasClass("no-sort")) {
            header.click(() => {
                if (header.hasClass("sorting-up")) {
                    location.hash = 'sort=d' + thisIndex;
                } else {
                    location.hash = 'sort=a' + thisIndex;
                }
                onHashChange();
            });
        }
        index++;
    }
    function clearSort() {
        headers.each(function() {
            $(this).removeClass("sorting-up");
            $(this).removeClass("sorting-down");
        });
    }
    function onHashChange() {
        if (location.hash == lastHash) return;
        lastHash = location.hash;
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
{{if .FirebaseConfig}}
<script src="https://www.gstatic.com/firebasejs/4.6.0/firebase.js"></script>
<script>
  // Initialize Firebase
  var config = {
    apiKey: "{{.FirebaseConfig.APIKey}}",
    authDomain: "{{.FirebaseConfig.ProjectID}}.firebaseapp.com",
    databaseURL: "https://{{.FirebaseConfig.ProjectID}}.firebaseio.com",
    projectId: "{{.FirebaseConfig.ProjectID}}",
    storageBucket: "{{.FirebaseConfig.ProjectID}}.appspot.com",
  };
  firebase.initializeApp(config);

  const database = firebase.database();
</script>
<script>
    const rows = tbody.find("tr");
    for (const rowElem of rows) {
        const row = $(rowElem);
        const checked = row.find('.states>.checked');
        const ref = database.ref("public/states/" + row.data("hash-key"));
        ref.on('value', snapshot => {
            const val = snapshot.val() || {};
            checked.data('lastState', !!val.checked);
            checked.prop('checked', !!val.checked);
            checked.prop('disabled', false);
        });
        checked.change(() => {
            if (checked.data('lastState') === checked.prop('checked')) return;
            checked.prop('disabled', true);
            ref.child('checked').set(checked.prop('checked'));
        });
    }
</script>
{{end}}
`

var (
	errWrongType    = errors.New("Wrong path type")
	listingTemplate *template.Template
)

var (
	PathHashHMACKey   = flag.String("path_hash_hmac_key", "", "A string (that will be hashed) key to be used for path hashes")
	FirebaseAPIKey    = flag.String("firebase_api_key", "", "Firebase API key to be used to store persistent state. Setting this enables Firebase")
	FirebaseProjectID = flag.String("firebase_project_id", "", "Firebase Project Id")
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

type firebaseConfig struct {
	APIKey    string
	ProjectID string
}

type fileInfoEx struct {
	os.FileInfo
	PathHashString string
}

type betterHttpListingServer struct {
	root       http.FileSystem
	fileServer http.Handler

	hmacKey []byte
}

func newBetterHttpListingServer(root http.FileSystem) http.Handler {
	var hmacKey []byte
	if *PathHashHMACKey != "" {
		hmacKeyArray := sha256.Sum256([]byte(*PathHashHMACKey))
		hmacKey = hmacKeyArray[:]
	}
	return &betterHttpListingServer{
		root:       root,
		fileServer: http.FileServer(root),
		hmacKey:    hmacKey,
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

	dirsEx := make([]fileInfoEx, len(dirs))
	for i, info := range dirs {
		dirsEx[i] = fileInfoEx{
			FileInfo: info,
		}
		if s.hmacKey != nil {
			h := hmac.New(sha256.New, s.hmacKey)
			h.Write([]byte(filepath.Join(p, dirs[i].Name())))
			mac := h.Sum(nil)
			dirsEx[i].PathHashString = base64.URLEncoding.EncodeToString(mac)
		}
	}

	var fbConfig *firebaseConfig
	if *FirebaseAPIKey != "" {
		fbConfig = &firebaseConfig{
			APIKey:    *FirebaseAPIKey,
			ProjectID: *FirebaseProjectID,
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	err = listingTemplate.Execute(w, struct {
		Path           string
		Dirs           []fileInfoEx
		FirebaseConfig *firebaseConfig
	}{
		p,
		dirsEx,
		fbConfig,
	})
	if err != nil {
		return err
	}

	return nil
}
