# Setup, not a page block: the further Decisions which-decisions-apply's
# examples show (D4 to D8, proposed) and the pins file its third block reads.
# Uses setup_call from places.sh, so it fails the same way.
i=3
for t in 'Round invoice totals to cents' 'General rule' 'Unplaced' 'Tokens follow the token spec' 'Pinned later'; do
  i=$((i + 1))
  setup_call create_decision "{\"title\":\"$t\",\"scope\":\"engineering\",\"body\":\"$t.\"}" "D$i" || return 1
done
rm -f setup-req.json
printf '[{"repository":"acme/app","commit":"0123456789abcdef0123456789abcdef01234567","path":"cmd/server/main.go","role":"implementation"}]' > pins.json
echo '(setup: D4 to D8 proposed, pins.json written)'
