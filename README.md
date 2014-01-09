rslurp
======

A simple full-directory HTTP downloader.

Copyright 2013 Google Inc. All Rights Reserved.
Apache 2.0 license.

This is NOT a Google product.

Contact: thomas@habets.se / habets@google.com
https://github.com/ThomasHabets/

Reason for existing
-------------------
wget -nd -np -r creates garbage files (index*), isn't parallel, and to my
knowledge can't restrict itself to a subset of files. Also it's spammy in its
status output.

Building
--------
In a temporary dir:
```
GOPATH="$(pwd)" go get github.com/ThomasHabets/rslurp/rslurp
cp bin/rslurp /usr/local/bin/
```

Example use
-----------
```
rslurp -workers=5 -matching='jpg$' http://foo.com/images/ http://foo.com/static/
```
