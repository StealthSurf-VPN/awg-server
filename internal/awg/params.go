package awg

import "fmt"

type AWGParams struct {
	Port int    `json:"port,omitempty"`
	Jc   int    `json:"jc,omitempty"`
	Jmin int    `json:"jmin,omitempty"`
	Jmax int    `json:"jmax,omitempty"`
	S1   int    `json:"s1,omitempty"`
	S2   int    `json:"s2,omitempty"`
	S3   int    `json:"s3,omitempty"`
	S4   int    `json:"s4,omitempty"`
	H1   uint32 `json:"h1,omitempty"`
	H2   uint32 `json:"h2,omitempty"`
	H3   uint32 `json:"h3,omitempty"`
	H4   uint32 `json:"h4,omitempty"`
	I1   string `json:"i1,omitempty"`
	I2   string `json:"i2,omitempty"`
	I3   string `json:"i3,omitempty"`
	I4   string `json:"i4,omitempty"`
	I5   string `json:"i5,omitempty"`
}

func (p AWGParams) Key() string {
	return fmt.Sprintf(
		"jc=%d,jmin=%d,jmax=%d,s1=%d,s2=%d,s3=%d,s4=%d,h1=%d,h2=%d,h3=%d,h4=%d,i1=%s,i2=%s,i3=%s,i4=%s,i5=%s",
		p.Jc, p.Jmin, p.Jmax,
		p.S1, p.S2, p.S3, p.S4,
		p.H1, p.H2, p.H3, p.H4,
		p.I1, p.I2, p.I3, p.I4, p.I5,
	)
}

func (p AWGParams) CLIArgs() []string {
	var args []string

	if p.Jc > 0 {
		args = append(args, "jc", fmt.Sprintf("%d", p.Jc))
	}

	if p.Jmin > 0 {
		args = append(args, "jmin", fmt.Sprintf("%d", p.Jmin))
	}

	if p.Jmax > 0 {
		args = append(args, "jmax", fmt.Sprintf("%d", p.Jmax))
	}

	if p.S1 > 0 {
		args = append(args, "s1", fmt.Sprintf("%d", p.S1))
	}

	if p.S2 > 0 {
		args = append(args, "s2", fmt.Sprintf("%d", p.S2))
	}

	if p.S3 > 0 {
		args = append(args, "s3", fmt.Sprintf("%d", p.S3))
	}

	if p.S4 > 0 {
		args = append(args, "s4", fmt.Sprintf("%d", p.S4))
	}

	if p.H1 > 0 {
		args = append(args, "h1", fmt.Sprintf("%d", p.H1))
	}

	if p.H2 > 0 {
		args = append(args, "h2", fmt.Sprintf("%d", p.H2))
	}

	if p.H3 > 0 {
		args = append(args, "h3", fmt.Sprintf("%d", p.H3))
	}

	if p.H4 > 0 {
		args = append(args, "h4", fmt.Sprintf("%d", p.H4))
	}

	if p.I1 != "" {
		args = append(args, "i1", p.I1)
	}

	if p.I2 != "" {
		args = append(args, "i2", p.I2)
	}

	if p.I3 != "" {
		args = append(args, "i3", p.I3)
	}

	if p.I4 != "" {
		args = append(args, "i4", p.I4)
	}

	if p.I5 != "" {
		args = append(args, "i5", p.I5)
	}

	return args
}

func (p AWGParams) ConfigLines() string {
	var lines string

	if p.Jc > 0 {
		lines += fmt.Sprintf("\nJc = %d", p.Jc)
	}

	if p.Jmin > 0 {
		lines += fmt.Sprintf("\nJmin = %d", p.Jmin)
	}

	if p.Jmax > 0 {
		lines += fmt.Sprintf("\nJmax = %d", p.Jmax)
	}

	if p.S1 > 0 {
		lines += fmt.Sprintf("\nS1 = %d", p.S1)
	}

	if p.S2 > 0 {
		lines += fmt.Sprintf("\nS2 = %d", p.S2)
	}

	if p.S3 > 0 {
		lines += fmt.Sprintf("\nS3 = %d", p.S3)
	}

	if p.S4 > 0 {
		lines += fmt.Sprintf("\nS4 = %d", p.S4)
	}

	if p.H1 > 0 {
		lines += fmt.Sprintf("\nH1 = %d", p.H1)
	}

	if p.H2 > 0 {
		lines += fmt.Sprintf("\nH2 = %d", p.H2)
	}

	if p.H3 > 0 {
		lines += fmt.Sprintf("\nH3 = %d", p.H3)
	}

	if p.H4 > 0 {
		lines += fmt.Sprintf("\nH4 = %d", p.H4)
	}

	if p.I1 != "" {
		lines += fmt.Sprintf("\nI1 = %s", p.I1)
	}

	if p.I2 != "" {
		lines += fmt.Sprintf("\nI2 = %s", p.I2)
	}

	if p.I3 != "" {
		lines += fmt.Sprintf("\nI3 = %s", p.I3)
	}

	if p.I4 != "" {
		lines += fmt.Sprintf("\nI4 = %s", p.I4)
	}

	if p.I5 != "" {
		lines += fmt.Sprintf("\nI5 = %s", p.I5)
	}

	return lines
}
