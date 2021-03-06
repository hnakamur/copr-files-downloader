package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/vbauerster/mpb"
	"github.com/vbauerster/mpb/decor"
)

func main() {
	destDir := flag.String("dest", "", "destination directory")
	user := flag.String("user", "", "user name")
	repo := flag.String("repo", "", "repository name")
	timeout := flag.Duration("timeout", time.Minute, "http client timeout")
	flag.Parse()

	buildURL, err := getLastBuildURL(*user, *repo)
	if err != nil {
		log.Fatal(err)
	}
	indexURLs, err := getIndexURLs(buildURL)
	if err != nil {
		log.Fatal(err)
	}
	fileURLs, err := getRPMFileURLs(indexURLs)
	if err != nil {
		log.Fatal(err)
	}
	err = downloadFiles(fileURLs, *timeout, *destDir)
	if err != nil {
		log.Fatal(err)
	}
}

func getLastBuildURL(user, repo string) (*url.URL, error) {
	packagesURL, err := url.Parse(fmt.Sprintf("https://copr.fedorainfracloud.org/coprs/%s/%s/", user, repo))
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocument(packagesURL.String())
	if err != nil {
		return nil, err
	}
	var buildURL *url.URL
	doc.Find("h3.panel-title").Each(func(i int, s *goquery.Selection) {
		if strings.TrimSpace(s.Text()) != "Last Build" {
			return
		}

		link := s.Parent().NextFiltered("div.list-group").Find("a")
		if link == nil {
			log.Fatal("Last Build link not found")
		}
		href, exists := link.Attr("href")
		if !exists {
			return
		}
		hrefURL, err := url.Parse(href)
		if err != nil {
			log.Println(err)
		}
		buildURL = doc.Url.ResolveReference(hrefURL)
	})
	if buildURL == nil {
		return nil, errors.New("last build not found for package")
	}
	return buildURL, nil
}

func getIndexURLs(buildURL *url.URL) ([]*url.URL, error) {
	doc, err := goquery.NewDocument(buildURL.String())
	if err != nil {
		return nil, err
	}
	var indexURLs []*url.URL
	doc.Find("h3.panel-title").Each(func(i int, s *goquery.Selection) {
		if strings.TrimSpace(s.Text()) != "Results" {
			return
		}

		s.Parent().NextFiltered("div.panel-body").Find("tr td:first-child a").
			Each(func(i int, s *goquery.Selection) {
				href, exists := s.Attr("href")
				if !exists {
					log.Printf("link href is empty")
				}
				hrefURL, err := url.Parse(href)
				if err != nil {
					log.Println(err)
				}
				indexURL := doc.Url.ResolveReference(hrefURL)
				indexURLs = append(indexURLs, indexURL)
			})
	})
	if indexURLs == nil {
		return nil, errors.New("No results found in Build page")
	}
	return indexURLs, nil
}

func getRPMFileURLs(indexURLs []*url.URL) ([]string, error) {
	var fileURLs []string
	for _, indexURL := range indexURLs {
		doc, err := goquery.NewDocument(indexURL.String())
		if err != nil {
			return nil, err
		}
		doc.Find("td.n a").Each(func(i int, s *goquery.Selection) {
			href, exists := s.Attr("href")
			if !exists || !strings.HasSuffix(href, ".rpm") {
				return
			}
			hrefURL, err := url.Parse(href)
			if err != nil {
				log.Println(err)
			}
			fileURL := doc.Url.ResolveReference(hrefURL)
			fileURLs = append(fileURLs, fileURL.String())
		})
	}
	return fileURLs, nil
}

func downloadFiles(fileURLs []string, timeout time.Duration, destDir string) error {
	if destDir == "" {
		var err error
		destDir, err = ioutil.TempDir("", "ppa")
		if err != nil {
			return err
		}
	} else {
		err := os.MkdirAll(destDir, 0700)
		if err != nil {
			return err
		}
	}

	var wg sync.WaitGroup
	p := mpb.New(mpb.WithWaitGroup(&wg))

	wg.Add(len(fileURLs))
	for _, fileURL := range fileURLs {
		fileURL := fileURL

		go func() {
			defer wg.Done()

			client := http.Client{Timeout: timeout}
			base := path.Base(fileURL)
			destFile := filepath.Join(destDir, base)
			file, err := os.Create(destFile)
			if err != nil {
				log.Println(err)
			}
			defer file.Close()

			resp, err := client.Get(fileURL)
			if err != nil {
				log.Println(err)
			}
			defer resp.Body.Close()

			contentLength := resp.ContentLength

			bar := p.AddBar(contentLength,
				mpb.PrependDecorators(
					decor.Name(base),
				),
				mpb.AppendDecorators(
					decor.Percentage(decor.WCSyncSpace),
				),
			)

			_, err = io.Copy(file, bar.ProxyReader(resp.Body))
			if err != nil {
				log.Println(err)
			}
		}()
	}
	p.Wait()
	log.Printf("downloaded files to %s", destDir)
	return nil
}
