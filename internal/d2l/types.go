package d2l

import (
	"cmp"
	"slices"
	"time"
)

// Course is one enrollment returned by myenrollments.
type Course struct {
	OrgUnit struct {
		ID   int    `json:"Id"`
		Name string `json:"Name"`
		Code string `json:"Code"`
		Type struct {
			ID   int    `json:"Id"`
			Code string `json:"Code"`
		} `json:"Type"`
		HomeURL string `json:"HomeUrl"`
	} `json:"OrgUnit"`
	Access struct {
		IsActive     bool       `json:"IsActive"`
		CanAccess    bool       `json:"CanAccess"`
		StartDate    *time.Time `json:"StartDate"`
		EndDate      *time.Time `json:"EndDate"`
		LastAccessed *time.Time `json:"LastAccessed"`
	} `json:"Access"`
	PinDate *time.Time `json:"PinDate"`
}

func (c Course) ID() int      { return c.OrgUnit.ID }
func (c Course) Name() string { return c.OrgUnit.Name }
func (c Course) Code() string { return c.OrgUnit.Code }
func (c Course) Pinned() bool { return c.PinDate != nil }
func (c Course) Current() bool {
	now := time.Now()
	if c.Access.EndDate != nil && c.Access.EndDate.Before(now) {
		return false
	}
	return c.Access.IsActive && c.Access.CanAccess
}

type paged[T any] struct {
	PagingInfo struct {
		Bookmark     string `json:"Bookmark"`
		HasMoreItems bool   `json:"HasMoreItems"`
	} `json:"PagingInfo"`
	Items []T `json:"Items"`
}

// RichText is D2L's {Text, Html} pair.
type RichText struct {
	Text string `json:"Text"`
	HTML string `json:"Html"`
}

// Activity types from the Valence content API.
const (
	ActivityFile    = 1
	ActivityLink    = 2
	ActivityDropbox = 3
	ActivityQuiz    = 4
	ActivityLTI     = 7
)

type Topic struct {
	TopicID          int        `json:"TopicId"`
	Title            string     `json:"Title"`
	TypeIdentifier   string     `json:"TypeIdentifier"` // "File" or "Link"
	URL              string     `json:"Url"`
	SortOrder        int        `json:"SortOrder"`
	ActivityType     int        `json:"ActivityType"`
	IsBroken         bool       `json:"IsBroken"`
	IsHidden         bool       `json:"IsHidden"`
	IsLocked         bool       `json:"IsLocked"`
	Unread           bool       `json:"Unread"`
	LastModifiedDate *time.Time `json:"LastModifiedDate"`
	StartDateTime    *time.Time `json:"StartDateTime"`
	EndDateTime      *time.Time `json:"EndDateTime"`
}

// IsFile reports whether the topic is backed by a downloadable file.
func (t Topic) IsFile() bool { return t.TypeIdentifier == "File" && !t.IsBroken }

// Available reports whether the user can open the topic at now. Learn lists
// topics that are locked or outside their dates but refuses to serve them.
func (t Topic) Available(now time.Time) bool {
	switch {
	case t.IsLocked:
		return false
	case t.StartDateTime != nil && now.Before(*t.StartDateTime):
		return false
	case t.EndDateTime != nil && now.After(*t.EndDateTime):
		return false
	}
	return true
}

type Module struct {
	ModuleID         int        `json:"ModuleId"`
	Title            string     `json:"Title"`
	SortOrder        int        `json:"SortOrder"`
	IsHidden         bool       `json:"IsHidden"`
	IsLocked         bool       `json:"IsLocked"`
	LastModifiedDate *time.Time `json:"LastModifiedDate"`
	Modules          []Module   `json:"Modules"`
	Topics           []Topic    `json:"Topics"`
}

type TOC struct {
	Modules []Module `json:"Modules"`
}

// Node is either a module or a topic, in display order.
type Node struct {
	Module *Module
	Topic  *Topic
}

func (n Node) Title() string {
	if n.Module != nil {
		return n.Module.Title
	}
	return n.Topic.Title
}

func (n Node) sortOrder() int {
	if n.Module != nil {
		return n.Module.SortOrder
	}
	return n.Topic.SortOrder
}

// Children returns submodules and topics merged by SortOrder.
func (m *Module) Children() []Node {
	out := make([]Node, 0, len(m.Modules)+len(m.Topics))
	for i := range m.Modules {
		out = append(out, Node{Module: &m.Modules[i]})
	}
	for i := range m.Topics {
		out = append(out, Node{Topic: &m.Topics[i]})
	}
	slices.SortStableFunc(out, func(a, b Node) int { return cmp.Compare(a.sortOrder(), b.sortOrder()) })
	return out
}

// Root returns the top-level modules as a synthetic module.
func (t *TOC) Root() *Module {
	return &Module{Title: "", Modules: t.Modules}
}

// FlatTopic is a topic with the module titles leading to it.
type FlatTopic struct {
	Path  []string // module titles, outermost first
	Topic Topic
}

// Walk returns every topic under m, depth first, in display order.
// prefix is prepended to each Path.
func (m *Module) Walk(prefix []string) []FlatTopic {
	var out []FlatTopic
	for _, n := range m.Children() {
		if n.Module != nil {
			p := append(slices.Clone(prefix), n.Module.Title)
			out = append(out, n.Module.Walk(p)...)
		} else {
			out = append(out, FlatTopic{Path: slices.Clone(prefix), Topic: *n.Topic})
		}
	}
	return out
}

type Attachment struct {
	FileID   int    `json:"FileId"`
	FileName string `json:"FileName"`
	Size     int64  `json:"FileSize"`
}

type NewsItem struct {
	ID          int          `json:"Id"`
	Title       string       `json:"Title"`
	Body        RichText     `json:"Body"`
	StartDate   *time.Time   `json:"StartDate"`
	IsHidden    bool         `json:"IsHidden"`
	IsPublished bool         `json:"IsPublished"`
	Attachments []Attachment `json:"Attachments"`
}

type DropboxFolder struct {
	ID                 int        `json:"Id"`
	Name               string     `json:"Name"`
	DueDate            *time.Time `json:"DueDate"`
	IsHidden           bool       `json:"IsHidden"`
	CustomInstructions RichText   `json:"CustomInstructions"`
	Availability       *struct {
		StartDate *time.Time `json:"StartDate"`
		EndDate   *time.Time `json:"EndDate"`
	} `json:"Availability"`
	Assessment *struct {
		ScoreDenominator *float64 `json:"ScoreDenominator"`
	} `json:"Assessment"`
}

// Submission status for one dropbox folder, from mysubmissions. Status is
// left out: the docs disagree on whether it is a number or a string, and
// Submissions and Feedback say all the app needs.
type MySubmission struct {
	Submissions []struct {
		ID             int        `json:"Id"`
		SubmissionDate *time.Time `json:"SubmissionDate"`
	} `json:"Submissions"`
	Feedback *struct {
		Score *float64 `json:"Score"`
	} `json:"Feedback"`
}

type GradeValue struct {
	GradeObjectIdentifier string   `json:"GradeObjectIdentifier"`
	GradeObjectName       string   `json:"GradeObjectName"`
	GradeObjectTypeName   string   `json:"GradeObjectTypeName"`
	DisplayedGrade        string   `json:"DisplayedGrade"`
	PointsNumerator       *float64 `json:"PointsNumerator"`
	PointsDenominator     *float64 `json:"PointsDenominator"`
	WeightedNumerator     *float64 `json:"WeightedNumerator"`
	WeightedDenominator   *float64 `json:"WeightedDenominator"`
	Comments              RichText `json:"Comments"`
}

type WhoAmI struct {
	Identifier string `json:"Identifier"`
	FirstName  string `json:"FirstName"`
	LastName   string `json:"LastName"`
	UniqueName string `json:"UniqueName"`
}
