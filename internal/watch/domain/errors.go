package domain

// Refusal is a broken rule, carrying one of the CLI's error codes and a hint
// naming the fix. Every inbound adapter translates it into its own terms.
type Refusal struct {
	code, message, hint string
}

func (r *Refusal) Error() string { return r.message }
func (r *Refusal) Code() string  { return r.code }
func (r *Refusal) Hint() string  { return r.hint }

// Refuse builds a refusal with a code from the CLI's table.
func Refuse(code, message, hint string) *Refusal {
	return &Refusal{code: code, message: message, hint: hint}
}

// Invalid is a value outside its form.
func Invalid(message, hint string) *Refusal { return Refuse("invalid_value", message, hint) }
