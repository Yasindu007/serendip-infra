package resource

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// KindBuild is one build of a Component's source into an image
// (specs/022-builds plan decision 5). It's owned by its Component, so the
// Component's scope governs who may trigger, cancel and read it, and it
// goes with the Component. Triggering is creating one; there's no delete
// of its own.
const KindBuild Kind = "Build"

// BuildSpec is what to build.
type BuildSpec struct {
	// Commit is the full commit ID to build. Empty means the head of the
	// Component's branch when the build starts.
	Commit string `json:"commit,omitempty"`
	// Cancel stops the build if it hasn't finished.
	Cancel bool `json:"cancel,omitempty"`
}

// BuildPhase is where a build is.
type BuildPhase string

const (
	BuildQueued    BuildPhase = "Queued"
	BuildRunning   BuildPhase = "Running"
	BuildSucceeded BuildPhase = "Succeeded"
	BuildFailed    BuildPhase = "Failed"
	BuildCancelled BuildPhase = "Cancelled"
)

// Done reports whether the phase is final.
func (p BuildPhase) Done() bool {
	return p == BuildSucceeded || p == BuildFailed || p == BuildCancelled
}

// BuildStatus is what the build did.
type BuildStatus struct {
	Status
	Phase BuildPhase `json:"phase,omitempty"`
	// Commit is the commit actually built.
	Commit string `json:"commit,omitempty"`
	// Image is the built image's reference, once it's pushed.
	Image      string      `json:"image,omitempty"`
	StartedAt  time.Time   `json:"startedAt,omitzero"`
	FinishedAt time.Time   `json:"finishedAt,omitzero"`
	Steps      []BuildStep `json:"steps,omitempty"`
	// Source is what the build builds: the Component's source as it was
	// when the build reconciler first saw the Build, so editing the
	// Component later doesn't change a build already asked for. AutoBuild
	// is never set on it.
	Source *ComponentSource `json:"source,omitempty"`
	// Started is set once the build's run was started on the platform;
	// before that, a Build is only waiting its turn.
	Started bool `json:"started,omitempty"`
}

// BuildStep is one step of a build, in the order they run.
type BuildStep struct {
	Name       string     `json:"name"`
	Phase      BuildPhase `json:"phase"`
	StartedAt  time.Time  `json:"startedAt,omitzero"`
	FinishedAt time.Time  `json:"finishedAt,omitzero"`
	Message    string     `json:"message,omitempty"`
}

type Build = Resource[BuildSpec, BuildStatus]

// commitRE is a full SHA-1 or SHA-256 commit ID: a remote can't be asked
// for an abbreviated one.
var commitRE = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ValidateBuild checks a Build is owned by exactly one Component, and its
// commit, if given, is a full commit ID. Whether the Component exists and
// builds from source is decided where Components are known.
func ValidateBuild(m Metadata, s BuildSpec) error {
	if BuildComponent(m) == "" {
		return fmt.Errorf("%w: metadata.ownerRefs: a Build is owned by exactly one Component", ErrValidation)
	}
	if s.Commit != "" && !commitRE.MatchString(s.Commit) {
		return fmt.Errorf("%w: commit: a full commit ID, 40 or 64 lower-case hex digits", ErrValidation)
	}
	return nil
}

// BuildComponent is the ID of the Component a Build belongs to, or "" if
// it isn't owned by exactly one Component and nothing else.
func BuildComponent(m Metadata) string {
	if len(m.OwnerRefs) != 1 || m.OwnerRefs[0].Kind != KindComponent || !validRefID(m.OwnerRefs[0].ID) {
		return ""
	}
	return m.OwnerRefs[0].ID
}

// ComponentSource is where a Component's image is built from (plan
// decision 6). Its values reach a build script, so each is held to a
// plain, shell-inert grammar.
type ComponentSource struct {
	// Repository is the Git repository's HTTPS or SSH URL.
	Repository string `json:"repository"`
	// Branch defaults to main.
	Branch string `json:"branch,omitempty"`
	// Path is the build context within the repository; it defaults to ".".
	Path string `json:"path,omitempty"`
	// Dockerfile is relative to Path; it defaults to Dockerfile.
	Dockerfile string `json:"dockerfile,omitempty"`
	// CredentialSecret is the ID of a Secret holding the repository's
	// username and password (or token), for a private repository.
	CredentialSecret string `json:"credentialSecret,omitempty"`
	// AutoBuild builds each new commit on the branch.
	AutoBuild bool `json:"autoBuild,omitempty"`
}

// WithDefaults fills the fields left empty.
func (s ComponentSource) WithDefaults() ComponentSource {
	if s.Branch == "" {
		s.Branch = "main"
	}
	if s.Path == "" {
		s.Path = "."
	}
	if s.Dockerfile == "" {
		s.Dockerfile = "Dockerfile"
	}
	return s
}

// SameBuild reports whether s and o build the same thing: the same
// repository, branch, path and Dockerfile. The credential and auto-build
// don't change what's built.
func (s ComponentSource) SameBuild(o ComponentSource) bool {
	s, o = s.WithDefaults(), o.WithDefaults()
	return s.Repository == o.Repository && s.Branch == o.Branch && s.Path == o.Path && s.Dockerfile == o.Dockerfile
}

// SSH reports whether the repository is reached over SSH, which can't be
// polled for new commits.
func (s ComponentSource) SSH() bool {
	return strings.HasPrefix(s.Repository, "ssh://") || scpLikeRE.MatchString(s.Repository)
}

const (
	maxRepositoryLen = 2048
	maxRepoPathLen   = 1024
)

var (
	// httpsRepoRE and sshRepoRE allow no credentials in the URL (they
	// belong in a Secret, except SSH's user name), no query and no
	// fragment, and nothing a shell would act on; an SCP-like address
	// can't start with "-", which git would read as an option.
	httpsRepoRE = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~%+-]+)+/?$`)
	sshRepoRE   = regexp.MustCompile(`^ssh://([A-Za-z0-9._-]+@)?[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~%+-]+)+/?$`)
	scpLikeRE   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9.-]+:[A-Za-z0-9._~%+-]+(/[A-Za-z0-9._~%+-]+)*/?$`)
	// branchRE is a simplified Git branch name; refName rejects the rest
	// of Git's rules that matter here.
	branchRE = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,255}$`)
	// repoPathRE is a relative path inside the repository.
	repoPathRE = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)

// ValidateComponentSource checks a Component has exactly one of an image
// and a source, and the source's own fields. Whether the credential
// Secret exists, and the build capability is ready, is decided where
// those are known.
func ValidateComponentSource(s ComponentSpec) error {
	switch {
	case s.Image != "" && s.Source != nil:
		return fmt.Errorf("%w: image and source: give one, not both", ErrValidation)
	case s.Image == "" && s.Source == nil:
		return fmt.Errorf("%w: image or source is required", ErrValidation)
	case s.Source == nil:
		return nil
	}
	src := s.Source.WithDefaults()
	r := src.Repository
	switch {
	case r == "":
		return fmt.Errorf("%w: source.repository is required", ErrValidation)
	case len(r) > maxRepositoryLen || !(httpsRepoRE.MatchString(r) || sshRepoRE.MatchString(r) || scpLikeRE.MatchString(r)):
		return fmt.Errorf("%w: source.repository: an https:// or SSH URL, without credentials, a query or a fragment", ErrValidation)
	case !refName(src.Branch):
		return fmt.Errorf("%w: source.branch %q isn't a branch name", ErrValidation, src.Branch)
	case !repoPath(src.Path, true):
		return fmt.Errorf("%w: source.path %q: a relative path inside the repository", ErrValidation, src.Path)
	case !repoPath(src.Dockerfile, false):
		return fmt.Errorf("%w: source.dockerfile %q: a relative path to a file", ErrValidation, src.Dockerfile)
	case src.CredentialSecret != "" && !validRefID(src.CredentialSecret):
		return fmt.Errorf("%w: source.credentialSecret: a Secret ID", ErrValidation)
	}
	return nil
}

func refName(b string) bool {
	if !branchRE.MatchString(b) || strings.HasPrefix(b, "-") || strings.HasSuffix(b, ".") || strings.Contains(b, "..") {
		return false
	}
	for part := range strings.SplitSeq(b, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// repoPath allows "." only where a directory is meant, a trailing "/"
// only on a directory, and never leaves the repository.
func repoPath(p string, dir bool) bool {
	if p == "." {
		return dir
	}
	if len(p) > maxRepoPathLen || !repoPathRE.MatchString(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") {
		return false
	}
	if dir {
		p = strings.TrimSuffix(p, "/")
	}
	for part := range strings.SplitSeq(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
