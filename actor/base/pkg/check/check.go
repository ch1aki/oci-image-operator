package check

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/k0kubun/pp"
	"github.com/sirupsen/logrus"
	"github.com/takutakahashi/oci-image-operator/actor/base/pkg/base"
	buildv1beta1 "github.com/takutakahashi/oci-image-operator/api/v1beta1"
	imageutil "github.com/takutakahashi/oci-image-operator/pkg/image"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CheckInput struct {
	Revisions []Revision `json:"revisions"`
}

type CheckOutput struct {
	Revisions []Revision `json:"revisions"`
}

type Revision struct {
	Registry         string                            `json:"registry"`
	ResolvedRevision string                            `json:"resolved_revision"`
	Revision         string                            `json:"revision"`
	Exist            buildv1beta1.ImageConditionStatus `json:"exist"`
}

type Check struct {
	c   client.Client
	ch  chan bool
	opt CheckOpt
	in  io.Writer
	out io.Reader
}

type CheckOpt struct {
	ImageName      string
	ImageNamespace string
	ImageTarget    string
	WatchPath      string
}

func (c CheckInput) Export(w io.Writer) error {
	if w == nil {
		var err error
		w, err = os.Create(base.InWorkDir("input"))
		if err != nil {
			return err
		}
	}
	logrus.Info("==== export input file ====")
	pp.Println(c)
	return base.ParseJSON(&c, w)
}

func ImportOutput(r io.Reader) (CheckOutput, error) {
	if r == nil {
		var err error
		r, err = os.Open(base.InWorkDir("output"))
		if err != nil {
			return CheckOutput{}, err
		}
	}
	c := CheckOutput{}
	err := base.MarshalJSON(&c, r)
	return c, err
}

func Init(cfg *rest.Config, opt CheckOpt) (*Check, error) {
	if cfg == nil {
		cfg = ctrl.GetConfigOrDie()
	}
	c, err := base.GenClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Check{
		c:   c,
		ch:  make(chan bool),
		opt: opt,
	}, nil
}

func (c *Check) Run(ctx context.Context) error {
	for {
		if err := c.Execute(ctx); err != nil {
			logrus.Error("error from execute")
			logrus.Error(err)
		} else {
			logrus.Info("finished")
			return nil
		}
		time.Sleep(10 * time.Second)
	}
}

func (c *Check) Stop() {
	logrus.Info("Stopping worker")
	if c.ch != nil {
		c.ch <- true
	}
}

// returns retry (bool) and error
func (c *Check) Execute(ctx context.Context) error {
	logrus.Info("start execute")
	image, err := base.GetImage(ctx, c.c, c.opt.ImageName, c.opt.ImageNamespace)
	if err != nil {
		return err
	}
	logrus.Info("start input func")
	conds := imageutil.GetConditionByStatus(image.Status.Conditions, buildv1beta1.ImageConditionTypeChecked, buildv1beta1.ImageConditionStatusFalse)
	if err := GetCheckInput(c.opt.ImageTarget, conds).Export(c.in); err != nil {
		return err
	}
	logrus.Info("start output func")
	output, err := ImportOutput(c.out)
	logrus.Info(output)
	if err != nil {
		return err
	}
	if err := c.UpdateImage(ctx, image, output); err != nil {
		return err
	}
	return nil
}

func (c *Check) UpdateImage(ctx context.Context, image *buildv1beta1.Image, output CheckOutput) error {
	logrus.Info("==== output ====")
	pp.Println(output)
	for _, rev := range output.Revisions {
		image.Status.Conditions = imageutil.UpdateCheckedCondition(
			image.Status.Conditions,
			buildv1beta1.ImageConditionStatusTrue,
			rev.Revision,
			rev.ResolvedRevision,
		)
		image.Status.Conditions = imageutil.UpdateUploadedCondition(
			image.Status.Conditions,
			rev.Exist,
			rev.Revision,
			rev.ResolvedRevision,
		)
	}
	return c.c.Status().Update(ctx, image, &client.UpdateOptions{})
}

func GetCheckInput(registry string, conds []buildv1beta1.ImageCondition) CheckInput {
	prs := []Revision{}
	for _, c := range conds {
		prs = append(prs, Revision{Registry: registry, ResolvedRevision: c.ResolvedRevision, Revision: c.Revision})
	}
	return CheckInput{
		Revisions: prs,
	}
}

func GetCheckOutput() CheckOutput {
	return CheckOutput{Revisions: []Revision{}}
}
