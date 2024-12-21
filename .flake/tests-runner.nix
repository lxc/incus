{ pkgs, ... }:
{
  services.ceph = {
    enable = true;
    global = {
      fsid = "e8f34a4a-4c96-4afe-b535-ceae0f70339d";
      monHost = "192.168.1.1";
      monInitialMembers = "mon0";
    };
    mon = {
      enable = true;
      daemons = [ "mon0" ];
    };
    mgr = {
      enable = true;
      daemons = [ "mgr0" ];
    };
    mds = {
      enable = true;
      daemons = [ "mds0" ];
    };
    osd = {
      enable = true;
      daemons = [ "0" "1" "2" ];
    };
  };

  virtualisation.emptyDiskImages = [
    2048
    2048
    2048
  ];

  environment.systemPackages = with pkgs; [
    ceph
  ];
}
