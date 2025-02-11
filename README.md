
# PRTG Grafana Datasource Plugin

This repository contains a Grafana datasource plugin for PRTG, allowing users to visualize and analyze PRTG metrics within Grafana.

## Introduction

This Grafana datasource plugin integrates with PRTG, enabling users to fetch and display data from PRTG sensors directly in Grafana dashboards. It provides a seamless way to monitor and analyze PRTG data using Grafana's powerful visualization tools.

## Installation

1. Clone the repository:
    ```sh
    git clone https://github.com/1DeliDolu/PRTG.git
    ```
2. Navigate to the plugin directory:
    ```sh
    cd PRTG
    ```
3. Install dependencies:
    ```sh
    npm install
    ```
4. Build the plugin:
    ```sh
    npm run build
    ```
5. Copy the plugin to Grafana's plugin directory:
    ```sh
    cp -r dist /var/lib/grafana/plugins/maxmarkusprogram-prtg-datasource
    ```
6. Restart Grafana:
    ```sh
    sudo systemctl restart grafana-server
    ```

## Configuration

1. Open Grafana and navigate to the Data Sources page.
2. Click on "Add data source" and select "PRTG".
3. Configure the PRTG datasource by providing the necessary connection details such as PRTG server URL, API key, and other relevant settings.
4. Save and test the datasource to ensure it is working correctly.

## Usage

1. Create a new dashboard or open an existing one in Grafana.
2. Add a new panel and select the PRTG datasource.
3. Configure the query to fetch data from the desired PRTG sensors.
4. Customize the visualization settings to display the data as needed.

## Troubleshooting

If you encounter any issues, please refer to the following troubleshooting steps:
- Ensure the PRTG server URL and API key are correctly configured.
- Check the Grafana server logs for any error messages.
- Verify that the plugin is correctly installed in Grafana's plugin directory.
- Restart Grafana and try again.

## Additional Resources

- [Grafana Plugin Development Documentation](https://grafana.com/developers/plugin-tools/)
- [PRTG API Documentation](https://www.paessler.com/manuals/prtg/api)

Feel free to contribute to this project by submitting issues or pull requests.

You can now save this content in the README.md file in your repository.
