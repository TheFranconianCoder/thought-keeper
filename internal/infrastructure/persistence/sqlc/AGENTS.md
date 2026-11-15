# sqlc

- Keep `--` comments in `queries/*.sql` ASCII. A typographic dash (—) makes sqlc's edited-query
  parser fail with `edited query syntax is invalid`; apostrophes and backticks are accepted. Use
  plain ASCII rather than guessing which punctuation the parser tolerates.
