{
  description = "wakewire: wakes an agent session when what it waits on changes on GitHub, GitLab or Jira";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      # A flake sees no tags, so the version is the one each release sets in
      # the mod's manifest, which the release checks against its tag.
      version = (builtins.fromJSON (builtins.readFile ./mod/.claude-plugin/plugin.json)).version;
    in
    {
      packages = forAll (pkgs: rec {
        wakewire = (pkgs.buildGoModule.override { go = pkgs.go_1_27; }) {
          pname = "wakewire";
          inherit version;
          src = self;
          subPackages = [ "cmd/wakewire" ];
          # Changes whenever go.mod does; CI builds the flake, so a stale one fails there.
          vendorHash = "sha256-Bgp8JKzlhAlCP4MDj19GtZLec2HWefk9RUNIhdmYHG8=";
          env.CGO_ENABLED = 0;
          ldflags = [ "-s" "-w" "-X main.version=v${version}" "-X main.commit=${self.rev or "dirty"}" ];
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
