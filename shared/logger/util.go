package logger

// WarnOnError calls the provided function and, if it returns an error, logs
// that error as a warning together with the provided message and optional
// context.
func WarnOnError(f func() error, msg string, ctx ...Ctx) {
	err := f()
	if err == nil {
		return
	}

	ctx = append(ctx, Ctx{"err": err})
	Warn(msg, ctx...)
}
