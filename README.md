golo - go, only less obsequious

golo lets you run go code that contains compile-time errors – these errors are converted to panics at runtime instead. It is inspired by Haskell's [deferred type
checking](https://downloads.haskell.org/~ghc/7.8.2/docs/html/users_guide/defer-type-errors.html).

This is useful for testing in-progress refactorings, or just letting you do the thing you wanted to do without having to listen to all the complaints from the go compiler.

To make Google-docs style collaboration for code a reality, we'll need tools that are just a little more forgiving:
who cares if your friend is in the middle of editing a function?
Not me! I want to test the code I just wrote…

# Installation & Usage

To install:

```
go install github.com/ConradIrwin/golo@latest
```

To use:

```
golo [-v] [run|test|build] [package]
```

You should be able to use `golo` in much the same way you use `go`.
For example, to run the tests for the current package: `golo test`.

# How does it work?

golo first tries to compile your code with `go`.
If that fails, it loads the broken files and packages and attempts to fix the errors (usually by replacing them with a `panic()`).
It then runs the `go` command again, but with an `-overlay` argument to the fixed versions of the files.
This repeats until all errors are fixed.

Some things the go compiler considers to be "errors" are just silently fixed
(this is partly because they're irritating, and partly because these errors are
often introduced deleting code to make it panic instead…):

- Unused imports
- Unused variables
- Using `:=` instead of `=` when there are no new variables

To see the kind of code that this can run, see the `examples/` directory.

# TODO

- It is currently quite slow, there's some easy wins untaken (reducing the number of loops by fixing more errors at a time; adding some caching), but also probably some larger more important fixes. Most of the time is from [`packages`](https://golang.org/x/tools/go/packages) package.
- It can't currently fix errors outside of function or method declarations. It would be nice so to do.
- There are some errors that could be fixed instead of panicking (e.g. missing a trailing , when line-wrapping a struct/function call).

# Meta-fu

golo is licensed under the MIT license. Contributions and bug-reports are welcome.
