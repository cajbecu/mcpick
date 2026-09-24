{
  description = "mcpick — choose which MCP servers a session loads, at the moment you launch it";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        version = "0.1.0";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "mcpick";
          inherit version;
          src = ./.;

          # Hash of the Go module dependencies. When go.mod or go.sum change,
          # set it to pkgs.lib.fakeHash, run `nix build`, and paste the "got:" hash.
          vendorHash = "sha256-erwlgjLz1LxzOgiEh9GTlpMJGVs6+kXNKQrH9At6Kko=";

          ldflags = [ "-s" "-w" "-X" "main.version=${version}" ];

          nativeBuildInputs = [ pkgs.installShellFiles ];
          postInstall = ''
            installManPage man/mcpick.1
            installShellCompletion \
              --bash completions/mcpick.bash \
              --zsh completions/_mcpick \
              --fish completions/mcpick.fish
          '';

          meta = with pkgs.lib; {
            description = "Interactive picker for MCP servers, with context-cost estimates";
            homepage = "https://github.com/cajbecu/mcpick";
            license = licenses.mit;
            mainProgram = "mcpick";
          };
        };

        devShells.default = pkgs.mkShell {
          packages = with pkgs; [ go gopls golangci-lint just goreleaser ];
        };
      });
}
