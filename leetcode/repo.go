package leetcode

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"github.com/invzhi/ankit"
)

// KeyFunc is the type of function called for each file or directory visited by filepath.Walk.
// The path argument is a relative path of Repo.path.
type KeyFunc func(path string, info os.FileInfo) (Key, error)

// CodeFunc is the type of function called for get leetcode question's code.
type CodeFunc func(path string, lang Lang) (string, error)

// Repo represents a repo which store leetcode solution code.
type Repo struct {
	path   string
	db     *sqlx.DB
	lang   Lang
	client http.Client

	KeyFn  KeyFunc
	CodeFn CodeFunc
}

// NewRepo create a anki deck for leetcode repo.
func NewRepo(path, dbfile string, lang Lang, codeFn CodeFunc, keyFn KeyFunc) *Repo {
	const schema = `
	CREATE TABLE IF NOT EXISTS questions (
		id           INTEGER PRIMARY KEY,
		title_slug   TEXT,
		title        TEXT DEFAULT '',
		content      TEXT DEFAULT '',
		difficulty   TEXT DEFAULT '',
		tags         TEXT DEFAULT '',
		code_snippet TEXT DEFAULT ''
	);
	CREATE UNIQUE INDEX IF NOT EXISTS questions_title_slug_index ON questions (title_slug)`

	db := sqlx.MustOpen("sqlite3", dbfile)
	db.MustExec(schema)

	r := Repo{
		db:     db,
		path:   path,
		lang:   lang,
		CodeFn: codeFn,
		KeyFn:  keyFn,
	}
	r.mustLoadKeys()

	return &r
}

// Notes returns all questions in Repo.
func (r *Repo) Notes() <-chan ankit.Note {
	notes := make(chan ankit.Note)

	go func() {
		err := filepath.Walk(r.path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			rel, _ := filepath.Rel(r.path, path)

			key, err := r.KeyFn(rel, info)
			if key != nil {
				notes <- r.Note(path, key)
			}

			return err
		})
		if err != nil {
			log.Printf("filepath.Walk error: %v", err)
		}

		close(notes)
	}()

	return notes
}

// Note returns a question in Repo with specific path and key.
func (r *Repo) Note(path string, key Key) ankit.Note {
	q := &question{repo: r}
	key(q)

	var err error

	switch {
	case q.ID != 0:
		err = q.getByID()
	case q.TitleSlug != "":
		err = q.getByTitleSlug()
	default:
		err = errors.New("leetcode.Question has no ID either TitleSlug")
	}

	if err != nil {
		q.err = err
		return q
	}

	if q.empty() {
		if err = q.fetch(); err != nil {
			q.err = err
			return q
		}
		if err = q.update(); err != nil {
			q.err = err
			return q
		}
	}

	q.Code, err = r.CodeFn(path, r.lang)
	if err != nil {
		q.err = err
		return q
	}

	return q
}

func (r *Repo) mustLoadKeys() {
	if err := r.loadKeys(); err != nil {
		panic(err)
	}
}

func (r *Repo) loadKeys() error {
	const url = "https://leetcode.com/api/problems/all/"

	log.Print("fetching id and title_slug from leetcode api...")

	resp, err := r.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var questions struct {
		StatStatusPairs []struct {
			Stat struct {
				FrontendQuestionID int    `json:"frontend_question_id"`
				QuestionTitleSlug  string `json:"question__title_slug"`
			} `json:"stat"`
		} `json:"stat_status_pairs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&questions); err != nil {
		return err
	}

	stmt, err := r.db.Prepare("INSERT OR IGNORE INTO questions(id, title_slug) VALUES(?, ?)")
	if err != nil {
		return err
	}

	for _, pair := range questions.StatStatusPairs {
		_, err = stmt.Exec(pair.Stat.FrontendQuestionID, pair.Stat.QuestionTitleSlug)
		if err != nil {
			return err
		}
	}

	return nil
}
