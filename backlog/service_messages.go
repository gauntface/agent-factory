package backlog

import (
	"errors"
	"fmt"
)

// PostMessage adds a note, an agent's question, or the user's answer to an
// item's thread. A question sets the session's attention; an answer is
// delivered to the session.
func (s *Service) PostMessage(req PostMessageRequest) (*MessageResponse, error) {
	switch req.Kind {
	case KindNote, KindQuestion, KindAnswer:
	default:
		return nil, fmt.Errorf("kind %q is not one of %s, %s and %s", req.Kind, KindNote, KindQuestion, KindAnswer)
	}
	p, err := s.resolve(req.RepoID, req.RepoPath)
	if err != nil {
		return nil, err
	}
	if req.Kind != KindQuestion && (req.Then != "" || req.Handoff != "") {
		return nil, errors.New("then and handoff go with a question")
	}
	switch req.Kind {
	case KindQuestion:
		return s.ask(p, req)
	case KindAnswer:
		return s.answer(p, req)
	}
	var msg Message
	_, err = s.update(p, func(b *Backlog) ([]int, error) {
		author := AuthorUser
		if req.SessionID != "" {
			it, err := b.get(req.ID)
			if err != nil {
				return nil, err
			}
			if !it.hasRun(req.SessionID) {
				return nil, fmt.Errorf("session %s is not a run of %s", req.SessionID, FormatID(req.ID))
			}
			author = "agent:" + req.SessionID
		}
		m, err := b.AddNote(req.ID, author, req.Text, s.now())
		if err != nil {
			return nil, err
		}
		msg = *m
		return []int{req.ID}, nil
	})
	if err != nil {
		return nil, err
	}
	return &MessageResponse{Message: msg}, nil
}

func (s *Service) ask(p *project, req PostMessageRequest) (*MessageResponse, error) {
	if req.SessionID == "" {
		return nil, errors.New("a question needs session_id: only the agent of the item's session asks — use \"af backlog ask --self\"")
	}
	if req.QuestionID != 0 {
		return nil, errors.New("question_id goes with an answer, not a question")
	}
	var msg Message
	_, err := s.update(p, func(b *Backlog) ([]int, error) {
		m, err := b.Ask(req.ID, req.SessionID, Question{Text: req.Text, Then: req.Then, Handoff: req.Handoff}, s.now())
		if err != nil {
			return nil, err
		}
		msg = *m
		if text := b.UserQuestionText(req.ID); text != "" {
			s.host.SetAttention(p.id, req.SessionID, text)
		}
		return []int{req.ID}, nil
	})
	if err != nil {
		return nil, err
	}
	return &MessageResponse{Message: msg}, nil
}

func (s *Service) answer(p *project, req PostMessageRequest) (*MessageResponse, error) {
	if req.SessionID != "" {
		return nil, errors.New("an answer comes from the user; it cannot carry session_id")
	}
	b, err := Load(p.id)
	if err != nil {
		return nil, err
	}
	it := b.Item(req.ID)
	if it == nil {
		return nil, p.named(&NotFoundError{ID: req.ID})
	}
	if b.IsParent(req.ID) {
		return s.answerGroup(p, req)
	}
	sessionID, target := "", TargetGone
	if last := it.lastRun(); last != nil {
		if last.State == RunStarting {
			return nil, fmt.Errorf("%s is starting a new session; answer once it is up", FormatID(req.ID))
		}
		if last.State == RunStarted && last.SessionID != "" {
			sessionID = last.SessionID
			target = s.host.AnswerTarget(p.id, sessionID)
		}
	}
	if target == TargetGone && !it.Status.Open() {
		return nil, fmt.Errorf("%s is %s; reopen it to start a new run with your answer", FormatID(req.ID), it.Status)
	}
	if target == TargetGone {
		msg, err := s.host.StartAnswerRun(p.id, AnswerRunRequest{RepoPath: req.RepoPath, ID: req.ID, QuestionID: req.QuestionID, Text: req.Text})
		if err != nil {
			return nil, p.named(err)
		}
		return &MessageResponse{Message: msg}, nil
	}
	var out Answered
	var pending PendingAnswer
	b, err = s.update(p, func(b *Backlog) ([]int, error) {
		var err error
		if out, err = b.Answer(req.ID, req.QuestionID, req.Text, true, s.now()); err != nil {
			return nil, err
		}
		if out.SessionID != sessionID {
			return nil, fmt.Errorf("%s changed while answering; retry", FormatID(req.ID))
		}
		next := ""
		if target == TargetLive {
			next = b.UserQuestionText(req.ID)
		}
		s.host.SetAttention(p.id, sessionID, next)
		it := b.Item(req.ID)
		pending = PendingAnswer{ID: req.ID, AnswerID: out.Answer.ID, SessionID: sessionID,
			Prompt: AnswerPrompt(it, out.Answer.Text), Handoff: it.handoff(out.Answer.Answers)}
		return []int{req.ID}, nil
	})
	if err != nil {
		return nil, err
	}
	s.host.DeliverAnswer(p.id, pending)
	return &MessageResponse{Message: out.Answer}, nil
}

// answerGroup answers a question af asked on a group. A group has no session
// to deliver to: the answer closes the question, and an orchestrator's failed
// deliveries are forgotten, so af tries to wake it again.
func (s *Service) answerGroup(p *project, req PostMessageRequest) (*MessageResponse, error) {
	var out Answered
	_, err := s.update(p, func(b *Backlog) ([]int, error) {
		var err error
		if out, err = b.Answer(req.ID, req.QuestionID, req.Text, false, s.now()); err != nil {
			return nil, err
		}
		if o := b.Item(req.ID).Orchestrator; o != nil {
			o.Failures = nil
		}
		return []int{req.ID}, nil
	})
	if err != nil {
		return nil, err
	}
	return &MessageResponse{Message: out.Answer}, nil
}
