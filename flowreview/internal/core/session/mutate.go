package session

import (
	"fmt"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// MarkReviewed marks hunk as reviewed, storing the body it had at head so a
// later Refresh can tell a moved hunk (same content, same id) from a changed
// one, and so the UI can diff "what you reviewed" against "what's there now".
func (s *Store) MarkReviewed(repoKey, id string, hunk model.DiffHunk, path, head string) error {
	return s.Update(repoKey, id, func(sess *model.ReviewSession) error {
		if sess.States == nil {
			sess.States = map[string]*model.ReviewState{}
		}
		sess.States[hunk.ID] = &model.ReviewState{
			HunkID:       hunk.ID,
			Path:         path,
			Status:       model.HunkReviewed,
			Body:         model.HunkBody(hunk.Lines),
			ReviewedAt:   time.Now().UTC(),
			ReviewedHead: head,
		}
		return nil
	})
}

// Unmark clears a hunk's reviewed state. There is no stored "unreviewed"
// status: absence from States means unreviewed, so unmarking removes the
// entry.
func (s *Store) Unmark(repoKey, id, hunkID string) error {
	return s.Update(repoKey, id, func(sess *model.ReviewSession) error {
		delete(sess.States, hunkID)
		return nil
	})
}

// UpsertComment inserts c as a new draft when c.ID is empty, assigning an id
// and timestamps, or replaces the existing draft with that id, preserving its
// original CreatedAt. It returns the comment as stored, so the caller learns
// the assigned id.
func (s *Store) UpsertComment(repoKey, id string, c model.CommentDraft) (model.CommentDraft, error) {
	var out model.CommentDraft
	err := s.Update(repoKey, id, func(sess *model.ReviewSession) error {
		now := time.Now().UTC()
		if c.ID == "" {
			c.ID = "c-" + randomHex(8)
			c.CreatedAt = now
			c.UpdatedAt = now
			sess.Comments = append(sess.Comments, c)
			out = c
			return nil
		}
		for i := range sess.Comments {
			if sess.Comments[i].ID == c.ID {
				c.CreatedAt = sess.Comments[i].CreatedAt
				c.UpdatedAt = now
				sess.Comments[i] = c
				out = c
				return nil
			}
		}
		return fmt.Errorf("session: comment %s not found", c.ID)
	})
	return out, err
}

// DeleteComment removes the draft with the given id. Deleting an id that is
// not there is not an error.
func (s *Store) DeleteComment(repoKey, id, commentID string) error {
	return s.Update(repoKey, id, func(sess *model.ReviewSession) error {
		kept := sess.Comments[:0]
		for _, c := range sess.Comments {
			if c.ID != commentID {
				kept = append(kept, c)
			}
		}
		sess.Comments = kept
		return nil
	})
}
