package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"text/template"

	filetype "gopkg.in/h2non/filetype.v1"

	"github.com/takama/daemon"
)

const (
	downloadPrefix = "/_download"
)

var (
	dirTemplate = template.Must(template.New("dirTemplate").Funcs(template.FuncMap{
		"join": filepath.Join,
	}).Parse(`<html>
<head>
<title>{{.title}}</title>
<style>
body {
  font-size: xx-large;
}
</style>
</head>
<body>
<ul>
{{$parent := .parent}}
{{range .files}}
{{if .BuildLink}}
<li><a href="{{join $parent .Name}}">{{join $parent .Name}}</a></li>
{{else}}
<li>{{join $parent .Name}}</li>
{{end}}
{{end}}
</ul>
</body>
</html>
`))
	fileTemplate = template.Must(template.New("fileTemplate").Funcs(template.FuncMap{
		"join": filepath.Join,
	}).Parse(`<head>
  <link href="http://vjs.zencdn.net/6.4.0/video-js.css" rel="stylesheet">

  <!-- If you'd like to support IE8 -->
  <script src="http://vjs.zencdn.net/ie8/1.1.2/videojs-ie8.min.js"></script>
</head>

<body>
  <video id="my-video" class="video-js" controls preload="auto" width="640" height="264"
  data-setup="{}">
    <source src="{{join .downloadPrefix .name}}" type='{{.type}}'>
    <p class="vjs-no-js">
      To view this video please enable JavaScript, and consider upgrading to a web browser that
      <a href="http://videojs.com/html5-video-support/" target="_blank">supports HTML5 video</a>
    </p>
  </video>

  <script src="http://vjs.zencdn.net/6.4.0/video.js"></script>
</body>
`))
)

type dirEntry struct {
	BuildLink bool
	Name      string
	Type      string
}

func handleDir(w http.ResponseWriter, r *http.Request, dir *os.File) {
	w.Header().Add("X-Mediaweb-Handler", "dir")
	infos, err := dir.Readdir(-1)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	entries := []dirEntry{}
	for _, info := range infos {
		if info.IsDir() {
			entries = append(entries, dirEntry{
				BuildLink: true,
				Name:      info.Name(),
				Type:      "directory",
			})
		} else {
			fileType, err := filetype.MatchFile(filepath.Join(dir.Name(), info.Name()))
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			entries = append(entries, dirEntry{
				BuildLink: fileType.MIME.Type == "video",
				Name:      info.Name(),
				Type:      fileType.Extension,
			})
		}
	}
	if err := dirTemplate.Execute(w, map[string]interface{}{
		"title":  dir.Name(),
		"files":  entries,
		"parent": filepath.Join("/", r.URL.Path),
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func handleFile(w http.ResponseWriter, r *http.Request, f *os.File) {
	w.Header().Add("X-Mediaweb-Handler", "file")
	fileType, err := filetype.MatchFile(f.Name())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Add("X-Mediaweb-Type", fmt.Sprintf("%+v", fileType))
	if err := fileTemplate.Execute(w, map[string]interface{}{
		"downloadPrefix": downloadPrefix,
		"name":           filepath.Join("/", r.URL.Path),
		"type":           fileType.MIME.Value,
	}); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func handleDownload(w http.ResponseWriter, r *http.Request, dir string) {
	w.Header().Add("X-Mediaweb-Handler", "download")
	realPath, err := filepath.Rel(downloadPrefix, r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	realPath, err = filepath.Abs(filepath.Join(dir, realPath))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !filepath.HasPrefix(realPath, dir) {
		http.Error(w, "outside allowed path", 400)
		return
	}
	fileType, err := filetype.MatchFile(realPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Add("Content-Type", fmt.Sprintf("%+v", fileType.MIME.Value))
	f, err := os.Open(realPath)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer f.Close()
	if _, err := io.Copy(w, f); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func handlerFunc(dir string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if filepath.HasPrefix(r.URL.Path, "/_download") {
			handleDownload(w, r, dir)
			return
		}
		realPath, err := filepath.Abs(filepath.Join(dir, r.URL.Path))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if !filepath.HasPrefix(realPath, dir) {
			http.Error(w, "outside allowed path", 400)
			return
		}
		f, err := os.Open(realPath)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if info.IsDir() {
			handleDir(w, r, f)
		} else {
			handleFile(w, r, f)
		}
	}
}

func run(hostPort string, dir string) {
	if err := http.ListenAndServe(hostPort, http.HandlerFunc(handlerFunc(dir))); err != nil {
		panic(err)
	}
}

func main() {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	dir := flag.String("dir", wd, "Which directory to serve.")
	hostPort := flag.String("host_port", "0.0.0.0:80", "Where to serve.")

	service, err := daemon.New("mediaweb", "Web server for media files.")
	if err != nil {
		log.Fatal("Error: ", err)
	}
	actions := map[string]func() (string, error){
		"install": func() (string, error) {
			return service.Install("-dir", *dir, "-host_port", *hostPort)
		},
		"remove": func() (string, error) {
			return service.Remove()
		},
		"status": func() (string, error) {
			return service.Status()
		},
		"start": func() (string, error) {
			return service.Start()
		},
		"stop": func() (string, error) {
			return service.Stop()
		},
	}
	possibleActions := []string{}
	for action := range actions {
		possibleActions = append(possibleActions, action)
	}

	action := flag.String("action", "", fmt.Sprintf("Which action to perform. One of %+v.", possibleActions))
	flag.Parse()

	if *action == "" {
		run(*hostPort, *dir)
		return
	}

	toPerform, found := actions[*action]
	if !found {
		flag.Usage()
		os.Exit(2)
	}
	status, err := toPerform()
	if err != nil {
		log.Fatal(status, "\nError: ", err)
	}
	fmt.Println(status)
}
