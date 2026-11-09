import { configure } from "@testing-library/react";

// The first render in a file is cold, and on a busy machine it can pass testing-library's one
// second to wait for something that was on its way.
configure({ asyncUtilTimeout: 5000 });
