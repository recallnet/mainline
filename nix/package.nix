{ lib
, buildGoModule
, version ? "dev"
, src
}:

buildGoModule {
  pname = "mainline";
  inherit version src;

  subPackages = [
    "cmd/mainline"
    "cmd/mq"
    "cmd/mainlined"
  ];

  vendorHash = "sha256-XKaF/91qyh/WXbrRBvbedpgMnVt4PsU3m354OuzbiFs=";

  ldflags = [
    "-s"
    "-w"
  ];

  meta = with lib; {
    description = "Local-first protected-branch coordinator for Git worktrees";
    homepage = "https://github.com/recallnet/mainline";
    platforms = platforms.unix;
    mainProgram = "mainline";
  };
}
