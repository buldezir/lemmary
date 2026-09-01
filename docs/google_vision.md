Getting a Google Cloud Vision API key involves a few specific steps within the Google Cloud Console. Because the Vision API is a paid service (though it includes a generous free tier of 1,000 free requests per month), you will need to set up a billing account to activate it.

Here is a step-by-step guide to generating and securing your API key.

---

## Step 1: Create a Google Cloud Project

All Google Cloud APIs must be tied to a specific project to manage tracking, quotas, and billing.

1. Go to the [Google Cloud Console](https://console.cloud.google.com/).
2. Log in with your Google account.
3. In the top-left header bar (next to the "Google Cloud" logo), click the **Project Dropdown** menu.
4. Click **New Project** in the upper-right corner of the popup window.
5. Enter a **Project Name** (e.g., `My-Vision-App`) and click **Create**.
6. Wait a few seconds for the project to provision, then make sure it is selected in the top dropdown menu.

---

## Step 2: Set Up a Billing Account

Google requires a linked billing account to activate the Vision API, even if you stay within the free tier.

1. Click the **Navigation Menu** (the three horizontal lines/hamburger icon in the top-left corner).
2. Select **Billing**.
3. If you don't have a billing account, click **Link a billing account** or **Create account**.
4. Follow the prompts to enter your country, profile, and a valid credit card.

> **Note:** Google provides a $300 free trial credit for new accounts, and you won't be charged unless you upgrade from Free Tier

---

## Step 3: Enable the Cloud Vision API

Now that your project has billing attached, you need to turn on the specific Vision functionality.

1. Open the Navigation Menu and go to **APIs & Services** > **Library**.
2. In the search bar, type **Cloud Vision API** and press Enter.
3. Click on the **Cloud Vision API** result.
4. Click the blue **Enable** button. (This may take a moment to process).

---

## Step 4: Generate Your API Key

Once the API is enabled, you can generate your actual credentials.

1. Go back to the Navigation Menu and select **APIs & Services** > **Credentials**.
2. Click the **+ Create Credentials** button at the top of the screen.
3. Select **API key** from the dropdown list.
4. A popup will appear displaying your new API key (a long string of letters and numbers).
5. Copy this key to your clipboard. Paste it into **Settings → Providers** as a `google_vision` provider, or into `.env` before the first boot as `OCR_API_KEY=`, alongside `OCR_SDK=google_vision`.
