# gamf - GitHub App Manifest Flow

This application enables you to programmatically generate GitHub Apps by
implementing the GitHub App Manifest Flow, so that you don't have to.

A hosted version is available at https://gamf.svc.bissy.io

## Endpoints

### POST /start

#### Request

This endpoint initiates an app creation flow. You must provide it with the
following keys, encoded as JSON:

`manifest`    - A JSON object, acceptable by GitHub's manifest flow. [docs][1]

`target_type` - The account type that this GitHub App should be created on (user, org).

`target_slug` - The account slug to create this GitHub App on.

`host`        - The GitHub instance to use (usually github.com).

#### Response

A JSON object containing the following keys will be returned

`url` - The URL to point your browser to, this will initiate the browser flow. [docs][2]

`key` - A unique one-time key that you will use at the end of this flow to
      retrieve the GitHub app information.

### POST /code/:key

This endpoint returns to you the GitHub provided code to be exchanged for the
app configuration. [docs][3]

#### Request

You must provide the following value as a URL parameter:

`key` - The key provided to you as part of the POST /start call.

#### Response

A JSON object containing the following keys will be returned:

`code` - The GitHub App Manifest code, to be used to retrieve you new app configuration.


[1]: https://docs.github.com/en/developers/apps/building-github-apps/creating-a-github-app-from-a-manifest#github-app-manifest-parameters
[2]: https://docs.github.com/en/developers/apps/building-github-apps/creating-a-github-app-from-a-manifest#1-you-redirect-people-to-github-to-create-a-new-github-app
[3]: https://docs.github.com/en/developers/apps/building-github-apps/creating-a-github-app-from-a-manifest#3-you-exchange-the-temporary-code-to-retrieve-the-app-configuration
