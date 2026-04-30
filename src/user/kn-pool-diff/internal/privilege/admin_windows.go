package privilege

import "golang.org/x/sys/windows"

type Status struct {
	Elevated bool
	Err      error
}

func Query() Status {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return Status{Err: err}
	}
	defer token.Close()

	return Status{Elevated: token.IsElevated()}
}
