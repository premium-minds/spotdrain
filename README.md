# Spotdrain

A daemon to drain nomad nodes upon arrival of a EC2 Spot Instance Interruption Notice.
Additionally, an event is sent to the Datadog API.

## How to run

```
$ export SPOTDRAIN_NOMAD_TOKEN='<nomad_token>' # Token for authentication with Nomad Node
$ export DD_CLIENT_APP_KEY="<dd_app_key>"
$ export DD_CLIENT_API_KEY="<dd_api_key>" # API and APP Keys for authentication with DD API
$ /path/to/spotdrain
```

## License

![GitHub](https://img.shields.io/github/license/premium-minds/spotdrain)

Copyright (C) 2023 [Premium Minds](http://www.premium-minds.com/)

Licensed under the [GNU Lesser General Public Licence](http://www.gnu.org/licenses/lgpl.html)