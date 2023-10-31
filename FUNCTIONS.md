# Kame ― Function Reference

## Variables

#### (let NAME VALUE?)

Declares the variable with the given `NAME` in the current scope, and
optionally initializes it with the given value:

Pre:

- `(not (defined? NAME :current-scope))`

Post:

- `(defined? NAME :current-scope)`


#### (get NAME)

Returns the current value of the variable `NAME`. The `NAME` needs to be
defined in the current scope and have a value bound.

Pre: 

- `(defined? NAME)`

#### (set NAME VALUE)

Sets the value of the variable `NAME` to the given `VALUE`.

Pre: 

- `(defined? NAME)`

Post: 

- `(eq (get NAME) VALUE)`

## Files

#### (find TEMPLATE)

#### (match? TEMPLATE VALUE)

#### (exists? PATH)

Returns true when the PATH exists

## Text

### `(rewrite FROMPAT TOPATH INPUT…)`

## Lists

#### (filter VALUE EXPR)

Keeps only the values in `VALUE` that match the `PATH` expressions.

#### (each )

#### (all )

Returns the content of the given parameter, recursively. This expands
to a flat list of values in depth-first traversal.

#### (slice VALUE START END)

Returns the `START` and `END` (must evaluate to integers) slice
of the given value (must evaluate to a list of map).

#### (sorted LIST)

Sorts the given values in ascending order

#### (uniq LIST)

Returns the list values without any duplicate.

#### (reverse LIST)

Reverse the given list of values

#### (count LIST)

#### (empty? )

Returns the number of elements

## Logical operators

#### (true)

#### (false)

#### (not )

#### (and )

#### (or )


## Communication

#### (shell )

#### (send )

#### (fail )

## Control

#### (if (EXPR EXPR‥) (EXPR EXPR‥) )

#### (match EXPR (case EXPR EXPR‥) (case EXPR EXPR‥) )

#### (for NAME EXPR EXPR‥)

## Logging

#### (log EXPR)

#### (error EXPR)

#### (warning EXPR)



