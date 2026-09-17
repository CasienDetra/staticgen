package site

import (
	"fmt"
	"strings"

	"github.com/casien/staticgen/internal/content"
)

func (a *Assembler) paginate(pages []*content.Page) []*content.Page {
	size := a.cfg.Build.Paginate
	if size <= 0 {
		return nil
	}
	var generated []*content.Page
	for _, first := range pages {
		if (first.Kind != content.KindHome && first.Kind != content.KindSection) || first.NoIndex || len(first.Pages) <= size {
			continue
		}
		items := first.Pages
		total := 1 + (len(items)-1)/size
		urlFor := func(number int) string {
			if number == 1 {
				return first.URL
			}
			return fmt.Sprintf("%s/page/%d/", strings.TrimSuffix(first.URL, "/"), number)
		}
		for number := 1; number <= total; number++ {
			p := first
			if number > 1 {
				later := *first
				p = &later
				p.URL = urlFor(number)
				p.OutputPath = a.mapper.OutputPath(p.URL)
				p.Permalink = a.permalink(p.URL)
				p.SourcePath = ""
				p.Meta.Aliases = nil
				p.BodyHTML, p.SummaryHTML, p.PlainText = "", "", ""
				p.Headings = nil
				p.WordCount, p.ReadingMinutes = 0, 0
				generated = append(generated, p)
			}
			start := (number - 1) * size
			p.Pages = items[start : start+min(size, len(items)-start)]
			p.Pagination = &content.Pagination{Number: number, TotalPages: total, TotalItems: len(items)}
			if number > 1 {
				p.Pagination.PrevURL = urlFor(number - 1)
			}
			if number < total {
				p.Pagination.NextURL = urlFor(number + 1)
			}
		}
	}
	return generated
}
