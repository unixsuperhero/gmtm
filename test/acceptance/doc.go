// Package acceptance holds the end-to-end acceptance suite (build tag
// "acceptance"). Every test runs against its own throwaway TUIOS server named
// cmt-<8hex> and stops only that server. Run it on vps2 with
// scripts/acceptance.sh.
package acceptance
