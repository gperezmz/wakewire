{
  description = "wakewire: wakes an agent session when what it waits on changes on GitHub, GitLab or Jira";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAll (pkgs: rec {
        wakewire = (pkgs.buildGoModule.override { go = pkgs.go_1_27; }) {
          pname = "wakewire";
          version = "unstable-${self.shortRev or "dirty"}";
          src = self;
          subPackages = [ "cmd/wakewire" ];
          # Changes whenever go.mod does; CI builds the flake, so a stale one fails there.
          vendorHash = "sha256-+A+CEGE+xpfD3c8k2kjZSYZT89jS5szKNYyrOmZlyeA=";
          env.CGO_ENABLED = 0;
          ldflags = [ "-s" "-w" "-X main.version=${self.shortRev or "dirty"}" "-X main.commit=${self.rev or "dirty"}" ];
          # The tests run in CI; some need git and a process table the sandbox lacks.
          doCheck = false;
          meta = {
            description = "Wakes an agent session when what it waits on changes on GitHub, GitLab or Jira";
            homepage = "https://github.com/gperezmz/wakewire";
            mainProgram = "wakewire";
          };
        };
        default = wakewire;
      });
    };
}
