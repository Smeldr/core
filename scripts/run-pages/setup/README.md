# Setups

A setup is a bash script a sequence runs between pages, for a step no page
shows a reader. It is named in a sequence as `{Setup: "name.sh"}`, printed in
the log as "not a page block", and fails the run on a non-zero exit.

None is in use. The places, the Decisions and the pins that `places.sh`,
`mcp.sh` and `wda.sh` made are now made by the pages themselves (Set up the
example places, and Find the Decisions that apply), so the pages are what is
run (T1272, T1274). Add a setup only for a step a reader truly never takes.
