This folder is what makes pingping portable.

pingping checks, at startup, whether a folder named "data" sits next to
pingping.exe. If it does, everything — the database, all history — stays in
here, nothing is written anywhere else on the machine, and no registry key is
touched. Deleting this folder is a complete uninstall.

If you remove this folder, pingping will instead use
%ProgramData%\pingping, which is what an installed copy does.

Nothing else in here needs your attention. You can delete this README.
